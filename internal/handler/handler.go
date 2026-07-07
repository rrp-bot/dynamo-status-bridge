// Package handler processes DynamoDB Stream events and writes status data to RDS.
package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	"github.com/psav/dynamo-status-bridge/internal/db"
	"github.com/psav/dynamo-status-bridge/internal/decoder"
)

// Handler dispatches DynamoDB stream records to the appropriate RDS writer methods.
type Handler struct {
	writer *db.Writer
}

// New creates a Handler backed by the given Writer.
func New(writer *db.Writer) *Handler {
	return &Handler{writer: writer}
}

// Handle processes a batch of DynamoDB stream records.
// It returns a DynamoDBEventResponse so that Lambda can perform partial batch
// failure reporting: only failed records are retried, not the whole batch.
//
// Within a batch, DynamoDB guarantees records are ordered oldest-to-newest
// within a shard. We deduplicate by documentID, keeping only the newest record
// for each item — intermediate updates carry no additional information.
// If the newest record for an item fails, all skipped older records for that
// same item are also reported as failures so Lambda retries them.
func (h *Handler) Handle(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	var response events.DynamoDBEventResponse

	// dedupe: iterate oldest-to-newest; later records overwrite earlier ones.
	// skipped maps documentID -> all sequence numbers superseded by a newer record.
	type entry struct {
		record   events.DynamoDBEventRecord
		skipped  []string // sequence numbers of older records for this documentID
	}
	seen := make(map[string]*entry, len(event.Records))

	for i := range event.Records {
		rec := event.Records[i]
		docID := recordDocumentID(rec)
		if docID == "" {
			// Can't deduplicate without a key — process immediately.
			if err := h.processRecord(ctx, rec); err != nil {
				slog.Error("failed to process record",
					"eventID", rec.EventID,
					"eventName", rec.EventName,
					"error", err,
				)
				response.BatchItemFailures = append(response.BatchItemFailures,
					events.DynamoDBBatchItemFailure{ItemIdentifier: rec.Change.SequenceNumber},
				)
			}
			continue
		}

		if existing, ok := seen[docID]; ok {
			slog.Debug("skipping superseded record",
				"documentID", docID,
				"skippedSeqNo", existing.record.Change.SequenceNumber,
				"newerSeqNo", rec.Change.SequenceNumber,
			)
			existing.skipped = append(existing.skipped, existing.record.Change.SequenceNumber)
			existing.record = rec
		} else {
			seen[docID] = &entry{record: rec}
		}
	}

	// Process one (newest) record per documentID.
	for docID, e := range seen {
		if err := h.processRecord(ctx, e.record); err != nil {
			slog.Error("failed to process record",
				"documentID", docID,
				"eventID", e.record.EventID,
				"eventName", e.record.EventName,
				"streamARN", e.record.EventSourceArn,
				"error", err,
			)
			// Report the failed record and all skipped predecessors — Lambda
			// will retry from the earliest failed sequence number.
			response.BatchItemFailures = append(response.BatchItemFailures,
				events.DynamoDBBatchItemFailure{ItemIdentifier: e.record.Change.SequenceNumber},
			)
			for _, seqNo := range e.skipped {
				response.BatchItemFailures = append(response.BatchItemFailures,
					events.DynamoDBBatchItemFailure{ItemIdentifier: seqNo},
				)
			}
		}
	}

	return response, nil
}

// recordDocumentID returns the documentID for a stream record, checking both
// NewImage (INSERT/MODIFY) and OldImage (REMOVE).
func recordDocumentID(rec events.DynamoDBEventRecord) string {
	if v, ok := rec.Change.NewImage["documentID"]; ok {
		return v.String()
	}
	if v, ok := rec.Change.OldImage["documentID"]; ok {
		return v.String()
	}
	return ""
}

// processRecord handles a single stream record.
func (h *Handler) processRecord(ctx context.Context, record events.DynamoDBEventRecord) error {
	streamARN := record.EventSourceArn

	desireType, err := decoder.DesireTypeFromStreamARN(streamARN)
	if err != nil {
		return fmt.Errorf("determining desire type: %w", err)
	}

	eventName := record.EventName

	slog.Info("processing record",
		"eventName", eventName,
		"desireType", desireType,
		"streamARN", streamARN,
		"sequenceNumber", record.Change.SequenceNumber,
	)

	switch desireType {
	case decoder.DesireTypeApply:
		return h.handleApply(ctx, eventName, streamARN, record.Change)
	case decoder.DesireTypeDelete:
		return h.handleDelete(ctx, eventName, streamARN, record.Change)
	case decoder.DesireTypeRead:
		return h.handleRead(ctx, eventName, streamARN, record.Change)
	default:
		return fmt.Errorf("unhandled desire type: %s", desireType)
	}
}

func (h *Handler) handleApply(ctx context.Context, eventName, streamARN string, change events.DynamoDBStreamRecord) error {
	switch eventName {
	case "INSERT", "MODIFY":
		status, err := decoder.DecodeApply(change.NewImage, streamARN)
		if err != nil {
			return fmt.Errorf("decoding apply desire: %w", err)
		}
		return h.writer.UpsertApply(ctx, status)

	case "REMOVE":
		documentID, managementCluster, err := extractKeys(change.OldImage)
		if err != nil {
			return fmt.Errorf("extracting keys from apply desire REMOVE: %w", err)
		}
		return h.writer.DeleteApply(ctx, documentID, managementCluster)

	default:
		return fmt.Errorf("unknown eventName: %s", eventName)
	}
}

func (h *Handler) handleDelete(ctx context.Context, eventName, streamARN string, change events.DynamoDBStreamRecord) error {
	switch eventName {
	case "INSERT", "MODIFY":
		status, err := decoder.DecodeDelete(change.NewImage, streamARN)
		if err != nil {
			return fmt.Errorf("decoding delete desire: %w", err)
		}
		return h.writer.UpsertDelete(ctx, status)

	case "REMOVE":
		documentID, managementCluster, err := extractKeys(change.OldImage)
		if err != nil {
			return fmt.Errorf("extracting keys from delete desire REMOVE: %w", err)
		}
		return h.writer.DeleteDelete(ctx, documentID, managementCluster)

	default:
		return fmt.Errorf("unknown eventName: %s", eventName)
	}
}

func (h *Handler) handleRead(ctx context.Context, eventName, streamARN string, change events.DynamoDBStreamRecord) error {
	switch eventName {
	case "INSERT", "MODIFY":
		status, err := decoder.DecodeRead(change.NewImage, streamARN)
		if err != nil {
			return fmt.Errorf("decoding read desire: %w", err)
		}
		return h.writer.UpsertRead(ctx, status)

	case "REMOVE":
		documentID, managementCluster, err := extractKeys(change.OldImage)
		if err != nil {
			return fmt.Errorf("extracting keys from read desire REMOVE: %w", err)
		}
		return h.writer.DeleteRead(ctx, documentID, managementCluster)

	default:
		return fmt.Errorf("unknown eventName: %s", eventName)
	}
}

// extractKeys pulls documentID and managementCluster from an image map.
// Used for REMOVE events where we only need the primary key.
func extractKeys(image map[string]events.DynamoDBAttributeValue) (documentID, managementCluster string, err error) {
	if v, ok := image["documentID"]; ok {
		documentID = v.String()
	}
	if documentID == "" {
		return "", "", fmt.Errorf("missing documentID in stream image")
	}

	// managementCluster lives inside the spec map.
	if spec, ok := image["spec"]; ok && spec.DataType() == events.DataTypeMap {
		if mc, ok := spec.Map()["managementCluster"]; ok {
			managementCluster = mc.String()
		}
	}
	if managementCluster == "" {
		return "", "", fmt.Errorf("missing managementCluster in stream image")
	}

	return documentID, managementCluster, nil
}

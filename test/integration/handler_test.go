package integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/psav/dynamo-status-bridge/internal/config"
	"github.com/psav/dynamo-status-bridge/internal/db"
	"github.com/psav/dynamo-status-bridge/internal/handler"
)

// requireIntegration skips the test unless POSTGRES_DSN is set.
// LOCALSTACK_ENDPOINT is not required for handler tests — we synthesize
// DynamoDB stream event payloads directly rather than using real streams.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("POSTGRES_DSN") == "" {
		t.Skip("skipping integration test: POSTGRES_DSN not set")
	}
}

func setupDB(t *testing.T) (*db.Client, *db.Writer) {
	t.Helper()
	cfg := &config.Config{
		UseIAMAuth: false,
		PlainDSN:   os.Getenv("POSTGRES_DSN"),
	}
	ctx := context.Background()
	client, err := db.New(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client, db.NewWriter(client.Pool())
}

func queryRow(t *testing.T, pool *pgxpool.Pool, query string, args ...any) map[string]any {
	t.Helper()
	rows, err := pool.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	fields := rows.FieldDescriptions()
	vals, err := rows.Values()
	if err != nil {
		t.Fatalf("scanning row: %v", err)
	}
	result := make(map[string]any, len(fields))
	for i, f := range fields {
		result[string(f.Name)] = vals[i]
	}
	return result
}

// makeStringAttr builds a DynamoDBAttributeValue of type String.
func makeStringAttr(s string) events.DynamoDBAttributeValue {
	return events.NewStringAttribute(s)
}

func makeNumberAttr(n string) events.DynamoDBAttributeValue {
	return events.NewNumberAttribute(n)
}

func makeMapAttr(m map[string]events.DynamoDBAttributeValue) events.DynamoDBAttributeValue {
	return events.NewMapAttribute(m)
}

func makeListAttr(l []events.DynamoDBAttributeValue) events.DynamoDBAttributeValue {
	return events.NewListAttribute(l)
}

// buildBaseImage constructs the common DynamoDB attribute map shared by all desire types.
func buildBaseImage(docID, mc, clusterID, streamARN string) map[string]events.DynamoDBAttributeValue {
	return map[string]events.DynamoDBAttributeValue{
		"documentID": makeStringAttr(docID),
		"version":    makeNumberAttr("1"),
		"updateTime": makeStringAttr(time.Now().UTC().Format(time.RFC3339Nano)),
		"spec": makeMapAttr(map[string]events.DynamoDBAttributeValue{
			"managementCluster": makeStringAttr(mc),
			"clusterID":         makeStringAttr(clusterID),
			"targetItem": makeMapAttr(map[string]events.DynamoDBAttributeValue{
				"group":    makeStringAttr("apps"),
				"version":  makeStringAttr("v1"),
				"resource": makeStringAttr("deployments"),
				"name":     makeStringAttr("my-deployment"),
			}),
		}),
		"status": makeMapAttr(map[string]events.DynamoDBAttributeValue{
			"conditions": makeListAttr([]events.DynamoDBAttributeValue{
				makeMapAttr(map[string]events.DynamoDBAttributeValue{
					"type":               makeStringAttr("Successful"),
					"status":             makeStringAttr("True"),
					"reason":             makeStringAttr("NoErrors"),
					"message":            makeStringAttr(""),
					"lastTransitionTime": makeStringAttr(time.Now().UTC().Format(time.RFC3339Nano)),
				}),
			}),
		}),
	}
}

func makeEvent(streamARN, eventName string, image map[string]events.DynamoDBAttributeValue) events.DynamoDBEvent {
	rec := events.DynamoDBEventRecord{
		EventName:      eventName,
		EventSourceArn: streamARN,
		Change: events.DynamoDBStreamRecord{
			SequenceNumber: "000001",
		},
	}
	if eventName == "REMOVE" {
		rec.Change.OldImage = image
	} else {
		rec.Change.NewImage = image
	}
	return events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_ReadDesire_UpsertAndDelete(t *testing.T) {
	requireIntegration(t)
	client, writer := setupDB(t)
	h := handler.New(writer)
	ctx := context.Background()

	streamARN := fmt.Sprintf("arn:aws:dynamodb:us-east-1:123456789012:table/mc01-status-readdesires/stream/%s", time.Now().Format("2006-01-02T15:04:05"))
	docID := fmt.Sprintf("test-read-%d", time.Now().UnixNano())

	image := buildBaseImage(docID, "mc01", "cluster-abc", streamARN)
	image["status_kubeContent"] = makeStringAttr(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"my-deployment"}}`)

	// INSERT
	resp, err := h.Handle(ctx, makeEvent(streamARN, "INSERT", image))
	if err != nil {
		t.Fatalf("Handle INSERT: %v", err)
	}
	if len(resp.BatchItemFailures) > 0 {
		t.Fatalf("unexpected batch failures on INSERT: %v", resp.BatchItemFailures)
	}

	row := queryRow(t, client.Pool(),
		`SELECT document_id, management_cluster, cluster_id, kube_content FROM read_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row == nil {
		t.Fatal("expected row in read_desire_statuses, got none")
	}
	if row["cluster_id"] != "cluster-abc" {
		t.Errorf("cluster_id: got %v, want cluster-abc", row["cluster_id"])
	}
	if row["kube_content"] == nil {
		t.Error("expected kube_content to be set")
	}

	// MODIFY — update kube_content to nil (object deleted from kube)
	image2 := buildBaseImage(docID, "mc01", "cluster-abc", streamARN)
	// no status_kubeContent key = nil kube_content
	resp, err = h.Handle(ctx, makeEvent(streamARN, "MODIFY", image2))
	if err != nil {
		t.Fatalf("Handle MODIFY: %v", err)
	}
	if len(resp.BatchItemFailures) > 0 {
		t.Fatalf("unexpected batch failures on MODIFY: %v", resp.BatchItemFailures)
	}

	row = queryRow(t, client.Pool(),
		`SELECT kube_content FROM read_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row["kube_content"] != nil {
		t.Errorf("expected kube_content NULL after MODIFY without kubeContent, got %v", row["kube_content"])
	}

	// REMOVE
	resp, err = h.Handle(ctx, makeEvent(streamARN, "REMOVE", image))
	if err != nil {
		t.Fatalf("Handle REMOVE: %v", err)
	}
	if len(resp.BatchItemFailures) > 0 {
		t.Fatalf("unexpected batch failures on REMOVE: %v", resp.BatchItemFailures)
	}

	row = queryRow(t, client.Pool(),
		`SELECT document_id FROM read_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row != nil {
		t.Error("expected row to be deleted, but it still exists")
	}
}

func TestIntegration_ApplyDesire_UpsertAndDelete(t *testing.T) {
	requireIntegration(t)
	client, writer := setupDB(t)
	h := handler.New(writer)
	ctx := context.Background()

	streamARN := fmt.Sprintf("arn:aws:dynamodb:us-east-1:123456789012:table/mc01-status-applydesires/stream/%s", time.Now().Format("2006-01-02T15:04:05"))
	docID := fmt.Sprintf("test-apply-%d", time.Now().UnixNano())

	image := buildBaseImage(docID, "mc01", "cluster-abc", streamARN)
	image["status"] = makeMapAttr(map[string]events.DynamoDBAttributeValue{
		"appliedResourceGeneration": makeNumberAttr("42"),
		"conditions":                makeListAttr(nil),
	})

	resp, err := h.Handle(ctx, makeEvent(streamARN, "INSERT", image))
	if err != nil {
		t.Fatalf("Handle INSERT: %v", err)
	}
	if len(resp.BatchItemFailures) > 0 {
		t.Fatalf("unexpected batch failures: %v", resp.BatchItemFailures)
	}

	row := queryRow(t, client.Pool(),
		`SELECT applied_resource_generation FROM apply_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row == nil {
		t.Fatal("expected row in apply_desire_statuses, got none")
	}
	if fmt.Sprintf("%v", row["applied_resource_generation"]) != "42" {
		t.Errorf("applied_resource_generation: got %v, want 42", row["applied_resource_generation"])
	}

	// REMOVE
	resp, err = h.Handle(ctx, makeEvent(streamARN, "REMOVE", image))
	if err != nil {
		t.Fatalf("Handle REMOVE: %v", err)
	}
	row = queryRow(t, client.Pool(),
		`SELECT document_id FROM apply_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row != nil {
		t.Error("expected row to be deleted")
	}
}

func TestIntegration_DeleteDesire_UpsertAndDelete(t *testing.T) {
	requireIntegration(t)
	client, writer := setupDB(t)
	h := handler.New(writer)
	ctx := context.Background()

	streamARN := fmt.Sprintf("arn:aws:dynamodb:us-east-1:123456789012:table/mc01-status-deletedesires/stream/%s", time.Now().Format("2006-01-02T15:04:05"))
	docID := fmt.Sprintf("test-delete-%d", time.Now().UnixNano())

	image := buildBaseImage(docID, "mc01", "cluster-abc", streamARN)

	resp, err := h.Handle(ctx, makeEvent(streamARN, "INSERT", image))
	if err != nil {
		t.Fatalf("Handle INSERT: %v", err)
	}
	if len(resp.BatchItemFailures) > 0 {
		t.Fatalf("unexpected batch failures: %v", resp.BatchItemFailures)
	}

	row := queryRow(t, client.Pool(),
		`SELECT document_id, cluster_id FROM delete_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row == nil {
		t.Fatal("expected row in delete_desire_statuses, got none")
	}

	resp, err = h.Handle(ctx, makeEvent(streamARN, "REMOVE", image))
	if err != nil {
		t.Fatalf("Handle REMOVE: %v", err)
	}
	row = queryRow(t, client.Pool(),
		`SELECT document_id FROM delete_desire_statuses WHERE document_id=$1 AND management_cluster=$2`,
		docID, "mc01",
	)
	if row != nil {
		t.Error("expected row to be deleted")
	}
}

func TestIntegration_BatchItemFailure_BadRecord(t *testing.T) {
	requireIntegration(t)
	_, writer := setupDB(t)
	h := handler.New(writer)
	ctx := context.Background()

	// A record with an unknown stream ARN suffix should produce a BatchItemFailure,
	// not a hard error, so the rest of the batch can proceed.
	badARN := "arn:aws:dynamodb:us-east-1:123456789012:table/mc01-status-unknown/stream/2024-01-01"
	event := events.DynamoDBEvent{
		Records: []events.DynamoDBEventRecord{
			{
				EventName:      "INSERT",
				EventSourceArn: badARN,
				Change: events.DynamoDBStreamRecord{
					SequenceNumber: "bad-seq-001",
					NewImage:       buildBaseImage("doc1", "mc01", "c1", badARN),
				},
			},
		},
	}

	resp, err := h.Handle(ctx, event)
	if err != nil {
		t.Fatalf("Handle should not return a hard error: %v", err)
	}
	if len(resp.BatchItemFailures) != 1 {
		t.Errorf("expected 1 BatchItemFailure, got %d", len(resp.BatchItemFailures))
	}
}

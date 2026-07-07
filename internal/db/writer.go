package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/psav/dynamo-status-bridge/internal/decoder"
)

// Writer performs upsert and delete operations against the three status tables.
type Writer struct {
	pool *pgxpool.Pool
}

// NewWriter creates a Writer backed by the given pool.
func NewWriter(pool *pgxpool.Pool) *Writer {
	return &Writer{pool: pool}
}

// UpsertApply inserts or updates a row in apply_desire_statuses.
func (w *Writer) UpsertApply(ctx context.Context, s *decoder.ApplyDesireStatus) error {
	conditions, err := marshalJSON(s.Conditions)
	if err != nil {
		return fmt.Errorf("marshalling conditions: %w", err)
	}
	targetItem, err := marshalJSON(s.TargetItem)
	if err != nil {
		return fmt.Errorf("marshalling target_item: %w", err)
	}

	_, err = w.pool.Exec(ctx, `
		INSERT INTO apply_desire_statuses (
			document_id, management_cluster, cluster_id, node_pool_name,
			dynamo_version, dynamo_update_time, dynamo_create_time,
			target_item, conditions, observed_desire_update_time,
			applied_resource_generation, stream_arn, last_updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, $12, now()
		)
		ON CONFLICT (document_id, management_cluster) DO UPDATE SET
			cluster_id                  = EXCLUDED.cluster_id,
			node_pool_name              = EXCLUDED.node_pool_name,
			dynamo_version              = EXCLUDED.dynamo_version,
			dynamo_update_time          = EXCLUDED.dynamo_update_time,
			dynamo_create_time          = EXCLUDED.dynamo_create_time,
			target_item                 = EXCLUDED.target_item,
			conditions                  = EXCLUDED.conditions,
			observed_desire_update_time = EXCLUDED.observed_desire_update_time,
			applied_resource_generation = EXCLUDED.applied_resource_generation,
			stream_arn                  = EXCLUDED.stream_arn,
			last_updated_at             = now()
	`,
		s.DocumentID, s.ManagementCluster, s.ClusterID, nilIfEmpty(s.NodePoolName),
		s.Version, s.UpdateTime, s.CreateTime,
		targetItem, conditions, s.ObservedDesireUpdateTime,
		s.AppliedResourceGeneration, s.StreamARN,
	)
	return err
}

// DeleteApply removes a row from apply_desire_statuses.
func (w *Writer) DeleteApply(ctx context.Context, documentID, managementCluster string) error {
	_, err := w.pool.Exec(ctx,
		`DELETE FROM apply_desire_statuses WHERE document_id = $1 AND management_cluster = $2`,
		documentID, managementCluster,
	)
	return err
}

// UpsertDelete inserts or updates a row in delete_desire_statuses.
func (w *Writer) UpsertDelete(ctx context.Context, s *decoder.DeleteDesireStatus) error {
	conditions, err := marshalJSON(s.Conditions)
	if err != nil {
		return fmt.Errorf("marshalling conditions: %w", err)
	}
	targetItem, err := marshalJSON(s.TargetItem)
	if err != nil {
		return fmt.Errorf("marshalling target_item: %w", err)
	}

	_, err = w.pool.Exec(ctx, `
		INSERT INTO delete_desire_statuses (
			document_id, management_cluster, cluster_id, node_pool_name,
			dynamo_version, dynamo_update_time, dynamo_create_time,
			target_item, conditions, observed_desire_update_time,
			stream_arn, last_updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, now()
		)
		ON CONFLICT (document_id, management_cluster) DO UPDATE SET
			cluster_id                  = EXCLUDED.cluster_id,
			node_pool_name              = EXCLUDED.node_pool_name,
			dynamo_version              = EXCLUDED.dynamo_version,
			dynamo_update_time          = EXCLUDED.dynamo_update_time,
			dynamo_create_time          = EXCLUDED.dynamo_create_time,
			target_item                 = EXCLUDED.target_item,
			conditions                  = EXCLUDED.conditions,
			observed_desire_update_time = EXCLUDED.observed_desire_update_time,
			stream_arn                  = EXCLUDED.stream_arn,
			last_updated_at             = now()
	`,
		s.DocumentID, s.ManagementCluster, s.ClusterID, nilIfEmpty(s.NodePoolName),
		s.Version, s.UpdateTime, s.CreateTime,
		targetItem, conditions, s.ObservedDesireUpdateTime,
		s.StreamARN,
	)
	return err
}

// DeleteDelete removes a row from delete_desire_statuses.
func (w *Writer) DeleteDelete(ctx context.Context, documentID, managementCluster string) error {
	_, err := w.pool.Exec(ctx,
		`DELETE FROM delete_desire_statuses WHERE document_id = $1 AND management_cluster = $2`,
		documentID, managementCluster,
	)
	return err
}

// UpsertRead inserts or updates a row in read_desire_statuses.
func (w *Writer) UpsertRead(ctx context.Context, s *decoder.ReadDesireStatus) error {
	conditions, err := marshalJSON(s.Conditions)
	if err != nil {
		return fmt.Errorf("marshalling conditions: %w", err)
	}
	targetItem, err := marshalJSON(s.TargetItem)
	if err != nil {
		return fmt.Errorf("marshalling target_item: %w", err)
	}

	// kube_content is already JSON (or nil).
	var kubeContent *json.RawMessage
	if s.KubeContent != nil {
		kubeContent = &s.KubeContent
	}

	_, err = w.pool.Exec(ctx, `
		INSERT INTO read_desire_statuses (
			document_id, management_cluster, cluster_id, node_pool_name,
			dynamo_version, dynamo_update_time, dynamo_create_time,
			target_item, conditions, observed_desire_update_time,
			kube_content, stream_arn, last_updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, $12, now()
		)
		ON CONFLICT (document_id, management_cluster) DO UPDATE SET
			cluster_id                  = EXCLUDED.cluster_id,
			node_pool_name              = EXCLUDED.node_pool_name,
			dynamo_version              = EXCLUDED.dynamo_version,
			dynamo_update_time          = EXCLUDED.dynamo_update_time,
			dynamo_create_time          = EXCLUDED.dynamo_create_time,
			target_item                 = EXCLUDED.target_item,
			conditions                  = EXCLUDED.conditions,
			observed_desire_update_time = EXCLUDED.observed_desire_update_time,
			kube_content                = EXCLUDED.kube_content,
			stream_arn                  = EXCLUDED.stream_arn,
			last_updated_at             = now()
	`,
		s.DocumentID, s.ManagementCluster, s.ClusterID, nilIfEmpty(s.NodePoolName),
		s.Version, s.UpdateTime, s.CreateTime,
		targetItem, conditions, s.ObservedDesireUpdateTime,
		kubeContent, s.StreamARN,
	)
	return err
}

// DeleteRead removes a row from read_desire_statuses.
func (w *Writer) DeleteRead(ctx context.Context, documentID, managementCluster string) error {
	_, err := w.pool.Exec(ctx,
		`DELETE FROM read_desire_statuses WHERE document_id = $1 AND management_cluster = $2`,
		documentID, managementCluster,
	)
	return err
}

// marshalJSON marshals v to a JSON byte slice suitable for a JSONB column,
// returning nil if v is the zero value or an empty slice.
func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" || string(b) == "[]" || string(b) == "{}" {
		return nil, nil
	}
	return b, nil
}

// nilIfEmpty returns nil for empty strings so they are stored as NULL.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

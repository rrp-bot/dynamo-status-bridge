-- dynamo-status-bridge: initial schema
-- Three tables, one per desire type. PK is (document_id, management_cluster)
-- because document_id is unique within a management cluster but may collide
-- across clusters.

-- ----------------------------------------------------------------------------
-- apply_desire_statuses
-- Populated from the {mc}-status-applydesires DynamoDB table.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS apply_desire_statuses (
    document_id                   TEXT        NOT NULL,
    management_cluster            TEXT        NOT NULL,
    cluster_id                    TEXT        NOT NULL,
    node_pool_name                TEXT,

    -- DynamoDB metadata
    dynamo_version                BIGINT      NOT NULL,
    dynamo_update_time            TIMESTAMPTZ NOT NULL,
    dynamo_create_time            TIMESTAMPTZ,

    -- Target resource reference (stored as JSONB for flexibility)
    target_item                   JSONB,

    -- Status fields
    conditions                    JSONB,
    observed_desire_update_time   TIMESTAMPTZ,
    applied_resource_generation   BIGINT,

    -- Provenance: which stream ARN delivered this record
    stream_arn                    TEXT        NOT NULL,
    last_updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (document_id, management_cluster)
);

-- ----------------------------------------------------------------------------
-- delete_desire_statuses
-- Populated from the {mc}-status-deletedesires DynamoDB table.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS delete_desire_statuses (
    document_id                   TEXT        NOT NULL,
    management_cluster            TEXT        NOT NULL,
    cluster_id                    TEXT        NOT NULL,
    node_pool_name                TEXT,

    -- DynamoDB metadata
    dynamo_version                BIGINT      NOT NULL,
    dynamo_update_time            TIMESTAMPTZ NOT NULL,
    dynamo_create_time            TIMESTAMPTZ,

    -- Target resource reference
    target_item                   JSONB,

    -- Status fields
    conditions                    JSONB,
    observed_desire_update_time   TIMESTAMPTZ,

    -- Provenance
    stream_arn                    TEXT        NOT NULL,
    last_updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (document_id, management_cluster)
);

-- ----------------------------------------------------------------------------
-- read_desire_statuses
-- Populated from the {mc}-status-readdesires DynamoDB table.
-- kube_content holds the full JSON of the mirrored Kubernetes object,
-- or NULL when the object does not exist on the management cluster.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS read_desire_statuses (
    document_id                   TEXT        NOT NULL,
    management_cluster            TEXT        NOT NULL,
    cluster_id                    TEXT        NOT NULL,
    node_pool_name                TEXT,

    -- DynamoDB metadata
    dynamo_version                BIGINT      NOT NULL,
    dynamo_update_time            TIMESTAMPTZ NOT NULL,
    dynamo_create_time            TIMESTAMPTZ,

    -- Target resource reference
    target_item                   JSONB,

    -- Status fields
    conditions                    JSONB,
    observed_desire_update_time   TIMESTAMPTZ,

    -- The full mirrored Kubernetes object (status_kubeContent from DynamoDB).
    -- NULL means the object does not exist on the management cluster.
    kube_content                  JSONB,

    -- Provenance
    stream_arn                    TEXT        NOT NULL,
    last_updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (document_id, management_cluster)
);

-- Index to support queries by management cluster across all tables.
CREATE INDEX IF NOT EXISTS idx_apply_desire_statuses_mc   ON apply_desire_statuses  (management_cluster);
CREATE INDEX IF NOT EXISTS idx_delete_desire_statuses_mc  ON delete_desire_statuses (management_cluster);
CREATE INDEX IF NOT EXISTS idx_read_desire_statuses_mc    ON read_desire_statuses   (management_cluster);

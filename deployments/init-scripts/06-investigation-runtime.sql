CREATE TABLE IF NOT EXISTS investigation_checkpoints (
 batch_id UUID PRIMARY KEY REFERENCES batches(id) ON DELETE CASCADE,
 snapshot BYTEA NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS investigation_artifacts (
 batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
 artifact_id TEXT NOT NULL, content BYTEA NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(batch_id,artifact_id)
);
CREATE TABLE IF NOT EXISTS investigation_executions (
 batch_id UUID PRIMARY KEY REFERENCES batches(id) ON DELETE CASCADE,
 owner UUID, generation BIGINT NOT NULL DEFAULT 0, lease_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

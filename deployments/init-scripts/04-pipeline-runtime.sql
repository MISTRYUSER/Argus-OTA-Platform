-- Additive runtime alignment; compatible with existing 01-03 deployments.
ALTER TABLE files ADD COLUMN IF NOT EXISTS error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE files ADD COLUMN IF NOT EXISTS completion_recorded BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE IF NOT EXISTS logs (
 id BIGSERIAL PRIMARY KEY, batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
 file_id UUID REFERENCES files(id) ON DELETE CASCADE, timestamp TIMESTAMP NOT NULL DEFAULT NOW(), log_content TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_batch ON logs(batch_id,timestamp);
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'completed';
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS aggregated_data JSONB NOT NULL DEFAULT '{}';
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS recommendations JSONB NOT NULL DEFAULT '[]';
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS confidence DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS diagnosed_at TIMESTAMP;
ALTER TABLE ai_diagnoses ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP NOT NULL DEFAULT NOW();
ALTER TABLE ai_diagnoses DROP CONSTRAINT IF EXISTS diagnoses_severity_check;
ALTER TABLE ai_diagnoses ADD CONSTRAINT diagnoses_severity_check CHECK (severity IN ('info','warning','error','critical','low','medium','high'));
CREATE TABLE IF NOT EXISTS event_outbox (
 id UUID PRIMARY KEY, event_key TEXT NOT NULL UNIQUE, batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
 event_type TEXT NOT NULL, payload JSONB NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT NOW(), published_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON event_outbox(created_at) WHERE published_at IS NULL;

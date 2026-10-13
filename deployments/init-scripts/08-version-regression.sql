-- Immutable parse products for uploaded files; statistics and event commit together.
CREATE TABLE IF NOT EXISTS parsed_file_data (
 file_id UUID PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
 batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
 observations JSONB NOT NULL CHECK(jsonb_typeof(observations)='array'),
 source TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_parsed_file_batch ON parsed_file_data(batch_id);
CREATE TABLE IF NOT EXISTS version_regression_reports (
 batch_id UUID PRIMARY KEY REFERENCES batches(id) ON DELETE CASCADE,
 report JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

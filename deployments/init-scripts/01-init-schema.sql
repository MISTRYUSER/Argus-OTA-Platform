-- Argus OTA Platform - PostgreSQL Schema Initialization
-- Version: 2.1

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Enable pgvector for RAG (optional, requires pgvector extension)
-- CREATE EXTENSION IF NOT EXISTS "vector";

-- ============================================================================
-- Batches Table
-- ============================================================================
-- Represents a batch upload task and its processing state

CREATE TABLE IF NOT EXISTS batches (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Metadata
    vehicle_id VARCHAR(255) NOT NULL,
    vin VARCHAR(255) NOT NULL,
    upload_time TIMESTAMP NOT NULL DEFAULT NOW(),
    -- Status tracking
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
        -- Possible values: 'pending', 'uploaded', 'scattering', 'scattered',
        --                 'gathering', 'gathered', 'diagnosing', 'completed', 'failed'
    total_files INTEGER NOT NULL DEFAULT 0,
    processed_files INTEGER NOT NULL DEFAULT 0,
    -- Barrier coordination
    expected_worker_count INTEGER NOT NULL,
    completed_worker_count INTEGER NOT NULL DEFAULT 0,
    -- MinIO storage
    minio_bucket VARCHAR(255),
    minio_prefix VARCHAR(255),
    -- Processing results
    error_message TEXT,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT batches_status_check CHECK (status IN (
        'pending', 'uploaded', 'scattering', 'scattered',
        'gathering', 'gathered', 'diagnosing', 'completed', 'failed'
    ))
);

-- Indexes for batches
CREATE INDEX IF NOT EXISTS idx_batches_vehicle_id ON batches(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_batches_vin ON batches(vin);
CREATE INDEX IF NOT EXISTS idx_batches_status ON batches(status);
CREATE INDEX IF NOT EXISTS idx_batches_upload_time ON batches(upload_time DESC);
CREATE INDEX IF NOT EXISTS idx_batches_created_at ON batches(created_at DESC);

-- ============================================================================
-- Files Table
-- ============================================================================

CREATE TABLE IF NOT EXISTS files (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Foreign key to batch
    batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    -- File metadata
    filename VARCHAR(255) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    file_size BIGINT NOT NULL,
    file_type VARCHAR(100),
    upload_time TIMESTAMP NOT NULL DEFAULT NOW(),
    -- Storage info
    minio_path VARCHAR(500) NOT NULL,
    minio_etag VARCHAR(255),
    -- Processing status
    processing_status VARCHAR(50) NOT NULL DEFAULT 'pending',
        -- Possible values: 'pending', 'parsing', 'parsed', 'aggregating', 'completed', 'failed'
    -- Metrics
    parse_duration_ms INTEGER,
    record_count INTEGER,
    -- Error tracking
    error_message TEXT,
    -- Timestamps
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT files_processing_status_check CHECK (processing_status IN (
        'pending', 'parsing', 'parsed', 'aggregating', 'completed', 'failed'
    ))
);

-- Indexes for files
CREATE INDEX IF NOT EXISTS idx_files_batch_id ON files(batch_id);
CREATE INDEX IF NOT EXISTS idx_files_filename ON files(filename);
CREATE INDEX IF NOT EXISTS idx_files_processing_status ON files(processing_status);
CREATE INDEX IF NOT EXISTS idx_files_upload_time ON files(upload_time DESC);

-- ============================================================================
-- AI Diagnoses Table
-- ============================================================================

CREATE TABLE IF NOT EXISTS ai_diagnoses (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Foreign key to batch
    batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    -- AI model info
    model_name VARCHAR(255) NOT NULL,
    model_version VARCHAR(100),
    -- Diagnosis result
    diagnosis_summary TEXT NOT NULL,
    severity VARCHAR(50),
        -- Possible values: 'info', 'warning', 'error', 'critical'
    -- Token usage (for cost tracking)
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    estimated_cost_usd DECIMAL(10, 4),
    -- Timing
    diagnosis_duration_ms INTEGER,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT diagnoses_severity_check CHECK (severity IN (
        'info', 'warning', 'error', 'critical'
    ))
);

-- Indexes for ai_diagnoses
CREATE INDEX IF NOT EXISTS idx_diagnoses_batch_id ON ai_diagnoses(batch_id);
CREATE INDEX IF NOT EXISTS idx_diagnoses_severity ON ai_diagnoses(severity);
CREATE INDEX IF NOT EXISTS idx_diagnoses_created_at ON ai_diagnoses(created_at DESC);

-- ============================================================================
-- Reports Table (Optional - for pre-aggregated reports)
-- ============================================================================

CREATE TABLE IF NOT EXISTS reports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Foreign key to batch
    batch_id UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    -- Report metadata
    report_type VARCHAR(100) NOT NULL,
        -- Possible values: 'system_health', 'error_analysis', 'performance', 'custom'
    -- JSON report data
    report_data JSONB NOT NULL,
    -- Cache control
    is_cached BOOLEAN DEFAULT false,
    cache_hit_count INTEGER DEFAULT 0,
    last_accessed_at TIMESTAMP,
    -- Timestamps
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for reports
CREATE INDEX IF NOT EXISTS idx_reports_batch_id ON reports(batch_id);
CREATE INDEX IF NOT EXISTS idx_reports_report_type ON reports(report_type);
CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at DESC);
-- GIN index for JSONB queries
CREATE INDEX IF NOT EXISTS idx_reports_report_data ON reports USING GIN (report_data);

-- ============================================================================
-- Functions and Triggers
-- ============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create triggers for updated_at
DROP TRIGGER IF EXISTS update_batches_updated_at ON batches;
CREATE TRIGGER update_batches_updated_at
    BEFORE UPDATE ON batches
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_files_updated_at ON files;
CREATE TRIGGER update_files_updated_at
    BEFORE UPDATE ON files
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_reports_updated_at ON reports;
CREATE TRIGGER update_reports_updated_at
    BEFORE UPDATE ON reports
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================================================
-- Initial Data
-- ============================================================================

-- Insert a demo batch for testing (optional)
INSERT INTO batches (
    vehicle_id,
    vin,
    status,
    total_files,
    expected_worker_count
) VALUES (
    'DEMO-VEHICLE-001',
    'DEMO-VIN-1234567890',
    'completed',
    3,
    5
) ON CONFLICT DO NOTHING;

-- ============================================================================
-- Comments for documentation
-- ============================================================================

COMMENT ON TABLE batches IS 'Stores batch upload tasks and their processing state';
COMMENT ON TABLE files IS 'Stores individual file information and processing status';
COMMENT ON TABLE ai_diagnoses IS 'Stores AI diagnosis results for each batch';
COMMENT ON TABLE reports IS 'Stores pre-aggregated reports for fast queries';

COMMENT ON COLUMN batches.status IS 'Current status of the batch processing pipeline';
COMMENT ON COLUMN batches.completed_worker_count IS 'Number of workers that have completed their tasks';
COMMENT ON COLUMN ai_diagnoses.severity IS 'Severity level of the diagnosis: info, warning, error, or critical';
COMMENT ON COLUMN reports.report_data IS 'JSONB formatted report data for flexible querying';

-- ============================================================================
-- Sample queries for reference
-- ============================================================================

-- Get all pending batches
-- SELECT * FROM batches WHERE status = 'pending' ORDER BY created_at DESC;

-- Get batch with all files
-- SELECT b.*, f.* FROM batches b LEFT JOIN files f ON b.id = f.batch_id WHERE b.id = 'batch-id';

-- Get AI diagnosis for a batch
-- SELECT * FROM ai_diagnoses WHERE batch_id = 'batch-id' ORDER BY created_at DESC LIMIT 1;

-- Search reports by JSONB data
-- SELECT * FROM reports WHERE report_data @> '{"severity": "critical"}';

-- Get batch statistics
-- SELECT
--     status,
--     COUNT(*) as count,
--     AVG(EXTRACT(EPOCH FROM (completed_at - created_at))) as avg_duration_seconds
-- FROM batches
-- GROUP BY status;

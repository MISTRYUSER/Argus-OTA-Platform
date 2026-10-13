-- Incident bindings are populated by trusted ingestion/operations, never by model tools.
CREATE TABLE IF NOT EXISTS incident_telemetry_scopes (
 batch_id UUID PRIMARY KEY REFERENCES batches(id) ON DELETE CASCADE,
 namespace TEXT NOT NULL CHECK(namespace ~ '^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$'),
 service_name TEXT NOT NULL CHECK(service_name ~ '^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$'),
 started_at TIMESTAMPTZ NOT NULL,
 ended_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CHECK (started_at >= '1970-01-01 UTC'::timestamptz AND ended_at > started_at AND ended_at <= started_at + INTERVAL '1 hour')
);
CREATE OR REPLACE FUNCTION reject_incident_scope_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'incident telemetry bindings are immutable';
END;
$$;
DROP TRIGGER IF EXISTS incident_scope_immutable ON incident_telemetry_scopes;
CREATE TRIGGER incident_scope_immutable BEFORE UPDATE ON incident_telemetry_scopes
 FOR EACH ROW EXECUTE FUNCTION reject_incident_scope_update();

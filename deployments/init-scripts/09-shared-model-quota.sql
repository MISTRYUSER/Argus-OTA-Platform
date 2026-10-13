-- Shared admission across worker processes; scope must identify one provider/account/model quota.
CREATE TABLE IF NOT EXISTS model_quota_scopes (
 scope TEXT PRIMARY KEY,
 capacity INTEGER NOT NULL CHECK (capacity BETWEEN 1 AND 128),
 request_ms BIGINT NOT NULL CHECK (request_ms > 0),
 grace_ms BIGINT NOT NULL CHECK (grace_ms > 0)
);
CREATE TABLE IF NOT EXISTS model_quota_permits (
 id UUID PRIMARY KEY,
 scope TEXT NOT NULL REFERENCES model_quota_scopes(scope),
 lock_id BIGINT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS model_quota_permits_scope ON model_quota_permits(scope, expires_at);

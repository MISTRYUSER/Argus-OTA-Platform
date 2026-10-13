package telemetryquery

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// NewPostgresScopeResolver only reads immutable incident bindings; absent bindings fail closed.
func NewPostgresScopeResolver(db *sql.DB) domain.ScopeResolver {
	return func(ctx context.Context, batch string) (domain.IncidentScope, error) {
		var s domain.IncidentScope
		if db == nil {
			return s, errors.New("incident scope database unavailable")
		}
		id, e := uuid.Parse(batch)
		if e != nil {
			return s, errors.New("invalid incident batch UUID")
		}
		e = db.QueryRowContext(ctx, `SELECT namespace, service_name, started_at, ended_at FROM incident_telemetry_scopes WHERE batch_id = $1`, id).Scan(&s.Namespace, &s.Service, &s.Start, &s.End)
		if e != nil {
			return s, e
		}
		return s, validateScope(s)
	}
}

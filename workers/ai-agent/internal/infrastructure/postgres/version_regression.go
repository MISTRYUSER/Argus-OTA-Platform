package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (r *PostgresDiagnosisRepository) GetVersionRegression(ctx context.Context, id string) (json.RawMessage, error) {
	var raw []byte
	e := r.db.QueryRowContext(ctx, `SELECT report FROM version_regression_reports WHERE batch_id=$1`, id).Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	return json.RawMessage(raw), e
}

package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
)

func (r *PostgresDiagnosisRepository) LoadInvestigationCheckpoint(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT snapshot FROM investigation_checkpoints WHERE batch_id=$1`, id).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return data, err
}
func (r *PostgresDiagnosisRepository) SaveInvestigationCheckpoint(ctx context.Context, id string, data []byte) error {
	if len(data) > 1024*1024 {
		return fmt.Errorf("checkpoint exceeds 1MB")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = validateExecution(ctx, tx, ctx, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO investigation_checkpoints(batch_id,snapshot) VALUES($1,$2) ON CONFLICT(batch_id) DO UPDATE SET snapshot=EXCLUDED.snapshot,updated_at=NOW()`, id, data)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *PostgresDiagnosisRepository) PutInvestigationArtifact(ctx context.Context, id string, data []byte) (string, error) {
	if len(data) > 1024*1024 {
		return "", fmt.Errorf("artifact exceeds 1MB")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	_, err := r.db.ExecContext(ctx, `INSERT INTO investigation_artifacts(batch_id,artifact_id,content) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, hash, data)
	return hash, err
}
func (r *PostgresDiagnosisRepository) GetInvestigationArtifact(ctx context.Context, id, artifactID string) ([]byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, err
	}
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT content FROM investigation_artifacts WHERE batch_id=$1 AND artifact_id=$2`, id, artifactID).Scan(&data)
	return data, err
}

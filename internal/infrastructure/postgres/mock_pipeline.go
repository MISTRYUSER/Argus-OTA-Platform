package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"time"
)

// MockParseBatch emits simulated analysis for actual uploaded file IDs. It is not a rec parser.
func MockParseBatch(ctx context.Context, db *sql.DB, id uuid.UUID) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, id)
	if err != nil {
		return err
	}
	if b.Status != domain.BatchStatusUploaded && b.Status != domain.BatchStatusScattering {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM files WHERE batch_id=$1 ORDER BY id`, id)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var f uuid.UUID
		if err = rows.Scan(&f); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, f := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE files SET error_code='E001',processing_status='parsed' WHERE id=$1`, f); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO logs(batch_id,file_id,log_content) SELECT $1,$2,'MOCK: E001 simulated connector timeout' WHERE NOT EXISTS(SELECT 1 FROM logs WHERE file_id=$2)`, id, f); err != nil {
			return err
		}
		if err = EnqueueEvent(ctx, tx, domain.FileParsed{BatchID: id, FileID: f, OccurredAt: time.Now()}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MockAggregateBatch checks durable file completion before publishing aggregation completion.
func MockAggregateBatch(ctx context.Context, db *sql.DB, id uuid.UUID) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, id)
	if err != nil {
		return err
	}
	if b.Status == domain.BatchStatusDiagnosing || b.Status == domain.BatchStatusCompleted {
		return nil
	}
	if b.Status != domain.BatchStatusGathering || b.TotalFiles < 1 || b.ProcessedFiles != b.TotalFiles {
		return fmt.Errorf("batch is not ready for aggregation")
	}
	if err = EnqueueEvent(ctx, tx, domain.GatheringCompleted{Version: "v1.0", BatchID: id, TotalFiles: b.TotalFiles, ChartFiles: []string{}, OccurredAt: time.Now()}); err != nil {
		return err
	}
	return tx.Commit()
}

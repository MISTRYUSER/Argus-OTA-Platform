package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

const batchColumns = `id,vehicle_id,vin,status,upload_time,total_files,processed_files,expected_worker_count,completed_worker_count,COALESCE(minio_bucket,''),COALESCE(minio_prefix,''),COALESCE(error_message,''),completed_at,created_at,updated_at`

func lockedBatch(ctx context.Context, tx *sql.Tx, id uuid.UUID) (*domain.Batch, error) {
	b := &domain.Batch{}
	err := tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM batches WHERE id=$1 FOR UPDATE`, id).Scan(&b.ID, &b.VehicleID, &b.VIN, &b.Status, &b.UploadTime, &b.TotalFiles, &b.ProcessedFiles, &b.ExpectedWorkerCount, &b.CompletedWorkerCount, &b.MinIOBucket, &b.MiniIOPrefix, &b.ErrorMessage, &b.CompletedAt, &b.CreatedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("batch not found: %s", id)
	}
	return b, err
}
func saveMutation(ctx context.Context, tx *sql.Tx, b *domain.Batch) error {
	_, err := tx.ExecContext(ctx, `UPDATE batches SET status=$2,total_files=$3,processed_files=$4,error_message=$5,completed_at=$6,updated_at=NOW() WHERE id=$1`, b.ID, b.Status, b.TotalFiles, b.ProcessedFiles, b.ErrorMessage, b.CompletedAt)
	if err != nil {
		return err
	}
	for _, event := range b.GetEvents() {
		if err := EnqueueEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}
func (r *PostgresBatchRepository) UpdateBatch(ctx context.Context, id uuid.UUID, mutate func(*domain.Batch) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = mutate(b); err != nil {
		return err
	}
	if err = saveMutation(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *PostgresBatchRepository) RecordFileCompletion(ctx context.Context, id, fileID uuid.UUID, mutate func(*domain.Batch) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, id)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE files SET completion_recorded=TRUE WHERE id=$1 AND batch_id=$2`, fileID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return &domain.InvalidEventError{Err: fmt.Errorf("file does not belong to batch")}
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE batch_id=$1 AND completion_recorded`, id).Scan(&b.ProcessedFiles); err != nil {
		return err
	}
	if err = mutate(b); err != nil {
		return err
	}
	if err = saveMutation(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *PostgresBatchRepository) SaveBatchAndFile(ctx context.Context, _ *domain.Batch, file *domain.File) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, file.BatchID)
	if err != nil {
		return err
	}
	if b.Status != domain.BatchStatusPending {
		return fmt.Errorf("upload is already closed")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO files(id,batch_id,filename,original_filename,file_size,file_type,minio_path,processing_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, file.ID, file.BatchID, file.Filename, file.OriginalFilename, file.FileSize, file.FileType, file.MinIOPath, file.ProcessingStatus)
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE batch_id=$1`, b.ID).Scan(&b.TotalFiles); err != nil {
		return err
	}
	if err = saveMutation(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresBatchRepository) CompleteDiagnosis(ctx context.Context, id, diagnosisID uuid.UUID, mutate func(*domain.Batch) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := lockedBatch(ctx, tx, id)
	if err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_diagnoses WHERE id=$1 AND batch_id=$2 AND status='completed')`, diagnosisID, id).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return &domain.InvalidEventError{Err: fmt.Errorf("diagnosis is missing, incomplete, or belongs to another batch")}
	}
	if err = mutate(b); err != nil {
		return err
	}
	if err = saveMutation(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

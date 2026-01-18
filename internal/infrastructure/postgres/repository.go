package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

type PostgresBatchRepository struct {
	db *sql.DB
}
func NewPostgresBatchRepository(db *sql.DB) domain.BatchRepository {
	return &PostgresBatchRepository{db: db}
}

func (r *PostgresBatchRepository)FindByVIN(ctx context.Context, vin string) ([]*domain.Batch, error) {
	return nil,nil
}
func (r *PostgresBatchRepository)List(ctx context.Context, opts domain.ListOptions) ([]*domain.Batch,error) {
	return nil,nil
}
func (r *PostgresBatchRepository) Save(ctx context.Context,batch *domain.Batch) error {
	query := `
          INSERT INTO batches (
              id, vehicle_id, vin, status, upload_time, 
              total_files, processed_files, expected_worker_count,
              completed_worker_count, minio_bucket, minio_prefix,
              error_message, completed_at, created_at, updated_at
          ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
          ON CONFLICT (id) DO UPDATE SET
              status = EXCLUDED.status,
              processed_files = EXCLUDED.processed_files,
              completed_worker_count = EXCLUDED.completed_worker_count,
              updated_at = EXCLUDED.updated_at
      `
	_, err := r.db.ExecContext(ctx,query,
		batch.ID, batch.VehicleID, batch.VIN, batch.Status.String(), batch.UploadTime,
		batch.TotalFiles, batch.ProcessedFiles, batch.ExpectedWorkerCount,
		batch.CompletedWorkerCount, batch.MinIOBucket, batch.MiniIOPrefix,
		batch.ErrorMessage, batch.CompletedAt, batch.CreatedAt, batch.UpdatedAt,
	)
	if err != nil {
		return err
	}
	return nil
}
func (r *PostgresBatchRepository) FindByID(ctx context.Context,id uuid.UUID) (*domain.Batch , error) {
	query := `
		SELECT id, vehicle_id,vin,status,upload_time,
			   total_files,processed_files,expected_worker_count,
			   completed_worker_count,minio_bucket,minio_prefix,
			   error_message,completed_at,created_at,updated_at
		FROM batches
		WHERE id = $1
	`
	var batch domain.Batch
	var statusStr string
	err := r.db.QueryRowContext(ctx,query,id).Scan(
		&batch.ID, &batch.VehicleID, &batch.VIN, &statusStr, &batch.UploadTime,
		&batch.TotalFiles, &batch.ProcessedFiles, &batch.ExpectedWorkerCount,
		&batch.CompletedWorkerCount, &batch.MinIOBucket, &batch.MiniIOPrefix,
		&batch.ErrorMessage, &batch.CompletedAt, &batch.CreatedAt, &batch.UpdatedAt,
	)
	batch.Status = domain.BatchStatus(statusStr)
	if err == sql.ErrNoRows {
		return nil,nil
	}
	if err != nil {
		return nil,err
	}
	return &batch,nil
}
func (r *PostgresBatchRepository) FindByStatus(ctx context.Context,status domain.BatchStatus) ([]*domain.Batch, error) {
	query := `
		SELECT id, vehicle_id,vin,status,upload_time,
			   total_files,processed_files,expected_worker_count,
			   completed_worker_count,minio_bucket,minio_prefix,
			   error_message,completed_at,created_at,updated_at
		FROM batches
		WHERE status = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx,query,status.String())
	if err != nil {
		return nil,err
	}
	defer rows.Close()
	var batches []*domain.Batch
	for rows.Next() {
		batch := &domain.Batch{}
		var statusStr string

		err := rows.Scan(
			&batch.ID, &batch.VehicleID, &batch.VIN, &statusStr, &batch.UploadTime,
			&batch.TotalFiles, &batch.ProcessedFiles, &batch.ExpectedWorkerCount,
			&batch.CompletedWorkerCount, &batch.MinIOBucket, &batch.MiniIOPrefix,
			&batch.ErrorMessage, &batch.CompletedAt, &batch.CreatedAt, &batch.UpdatedAt,
		)
		if err != nil{
			return nil,err
		}

		batch.Status = domain.BatchStatus(statusStr)
		batches = append(batches,batch)
	}
	return batches,nil
}

func (r *PostgresBatchRepository) Delete (ctx context.Context,id uuid.UUID) error {
	query := `
		DELETE  FROM batches 
		WHERE id = $1 
	`
	result,err := r.db.ExecContext(ctx,query,id)
	if err != nil {
		return err 
	}
	rowAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowAffected == 0  {
		return errors.New("batch not found")
	}
	return nil
}
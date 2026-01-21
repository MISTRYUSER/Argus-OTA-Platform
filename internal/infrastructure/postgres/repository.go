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
              total_files = EXCLUDED.total_files,
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

// ============================================================================
// FileRepository Implementation
// ============================================================================

type PostgresFileRepository struct {
	db *sql.DB
}

func NewPostgresFileRepository(db *sql.DB) domain.FileRepository {
	return &PostgresFileRepository{db: db}
}

func (r *PostgresFileRepository) Save(ctx context.Context, file *domain.File) error {
	query := `
		INSERT INTO files (
			id, batch_id, filename, original_filename, file_size, file_type,
			upload_time, minio_path, minio_etag, processing_status,
			parse_duration_ms, record_count, error_message, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (id) DO UPDATE SET
			processing_status = EXCLUDED.processing_status,
			parse_duration_ms = EXCLUDED.parse_duration_ms,
			record_count = EXCLUDED.record_count,
			error_message = EXCLUDED.error_message,
			updated_at = EXCLUDED.updated_at
	`
	_, err := r.db.ExecContext(ctx, query,
		file.ID, file.BatchID, file.Filename, file.OriginalFilename, file.FileSize, file.FileType,
		file.UploadTime, file.MinIOPath, file.MinIOETag, file.ProcessingStatus.String(),
		file.ParseDurationMs, file.RecordCount, file.ErrorMessage, file.CreatedAt, file.UpdatedAt,
	)
	if err != nil {
		return err
	}
	return nil
}

func (r *PostgresFileRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.File, error) {
	query := `
		SELECT id, batch_id, filename, original_filename, file_size, file_type,
			   upload_time, minio_path, minio_etag, processing_status,
			   parse_duration_ms, record_count, error_message, created_at, updated_at
		FROM files
		WHERE id = $1
	`
	var file domain.File
	var statusStr string
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&file.ID, &file.BatchID, &file.Filename, &file.OriginalFilename, &file.FileSize, &file.FileType,
		&file.UploadTime, &file.MinIOPath, &file.MinIOETag, &statusStr,
		&file.ParseDurationMs, &file.RecordCount, &file.ErrorMessage, &file.CreatedAt, &file.UpdatedAt,
	)
	file.ProcessingStatus = domain.ProcessingStatus(statusStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &file, nil
}

func (r *PostgresFileRepository) FindByBatchID(ctx context.Context, batchID uuid.UUID) ([]*domain.File, error) {
	query := `
		SELECT id, batch_id, filename, original_filename, file_size, file_type,
			   upload_time, minio_path, minio_etag, processing_status,
			   parse_duration_ms, record_count, error_message, created_at, updated_at
		FROM files
		WHERE batch_id = $1
		ORDER BY upload_time DESC
	`
	rows, err := r.db.QueryContext(ctx, query, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		file := &domain.File{}
		var statusStr string

		err := rows.Scan(
			&file.ID, &file.BatchID, &file.Filename, &file.OriginalFilename, &file.FileSize, &file.FileType,
			&file.UploadTime, &file.MinIOPath, &file.MinIOETag, &statusStr,
			&file.ParseDurationMs, &file.RecordCount, &file.ErrorMessage, &file.CreatedAt, &file.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		file.ProcessingStatus = domain.ProcessingStatus(statusStr)
		files = append(files, file)
	}
	return files, nil
}

func (r *PostgresFileRepository) UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status domain.ProcessingStatus) error {
	query := `
		UPDATE files
		SET processing_status = $1, updated_at = NOW()
		WHERE id = $2
	`
	result, err := r.db.ExecContext(ctx, query, status.String(), id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return errors.New("file not found")
	}

	return nil
}
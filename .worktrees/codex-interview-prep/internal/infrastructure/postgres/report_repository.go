package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

const defaultReportType = "batch_report"

// PostgresReportRepository implements domain.ReportRepository using the reports table.
// report_data stores the full domain.Report JSON for flexible querying.
type PostgresReportRepository struct {
	db *sql.DB
}

func NewPostgresReportRepository(db *sql.DB) domain.ReportRepository {
	return &PostgresReportRepository{db: db}
}

func (r *PostgresReportRepository) Save(ctx context.Context, report *domain.Report) error {
	if report == nil {
		return errors.New("report is nil")
	}

	// Try to reuse existing report ID for this batch to avoid duplicates.
	var existingID uuid.UUID
	var existingCreatedAt time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT id, created_at FROM reports WHERE batch_id = $1 ORDER BY updated_at DESC LIMIT 1`,
		report.BatchID,
	).Scan(&existingID, &existingCreatedAt)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to query existing report: %w", err)
	}
	if err == nil {
		report.ID = existingID
		if report.CreatedAt.IsZero() {
			report.CreatedAt = existingCreatedAt
		}
	}

	if report.ID == uuid.Nil {
		report.ID = uuid.New()
	}

	now := time.Now()
	if report.CreatedAt.IsZero() {
		report.CreatedAt = now
	}
	report.UpdatedAt = now

	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	query := `
		INSERT INTO reports (
			id, batch_id, report_type, report_data,
			is_cached, cache_hit_count, last_accessed_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			report_type = EXCLUDED.report_type,
			report_data = EXCLUDED.report_data,
			is_cached = EXCLUDED.is_cached,
			cache_hit_count = EXCLUDED.cache_hit_count,
			last_accessed_at = EXCLUDED.last_accessed_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.db.ExecContext(ctx, query,
		report.ID,
		report.BatchID,
		defaultReportType,
		data,
		false,
		0,
		now,
		report.CreatedAt,
		report.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save report: %w", err)
	}

	return nil
}

func (r *PostgresReportRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Report, error) {
	query := `SELECT report_data FROM reports WHERE id = $1`

	var raw []byte
	err := r.db.QueryRowContext(ctx, query, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query report: %w", err)
	}

	var report domain.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal report_data: %w", err)
	}
	if report.ID == uuid.Nil {
		report.ID = id
	}

	return &report, nil
}

func (r *PostgresReportRepository) FindByBatchID(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	query := `
		SELECT id, report_data
		FROM reports
		WHERE batch_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`

	var id uuid.UUID
	var raw []byte
	err := r.db.QueryRowContext(ctx, query, batchID).Scan(&id, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query report by batch_id: %w", err)
	}

	var report domain.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal report_data: %w", err)
	}
	if report.ID == uuid.Nil {
		report.ID = id
	}

	return &report, nil
}

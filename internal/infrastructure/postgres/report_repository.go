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

var reportIDNamespace = uuid.MustParse("5a6d5151-78cc-4fc2-b2f3-f26ab3114e4d")

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
		report.ID = batchReportID(report.BatchID)
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

func batchReportID(batchID uuid.UUID) uuid.UUID {
	if batchID == uuid.Nil {
		return uuid.New()
	}
	return uuid.NewSHA1(reportIDNamespace, []byte(defaultReportType+":"+batchID.String()))
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

// Read current batch state and its durable diagnosis, never an old report snapshot.
func (r *PostgresReportRepository) FindByBatchID(ctx context.Context, id uuid.UUID) (*domain.Report, error) {
	report := &domain.Report{BatchID: id, ID: batchReportID(id)}
	var diagnosisID uuid.NullUUID
	var summary sql.NullString
	var investigation []byte
	err := r.db.QueryRowContext(ctx, `SELECT b.vehicle_id,b.vin,b.status,b.total_files,b.processed_files,b.created_at,b.updated_at,d.id,d.diagnosis_summary,COALESCE(d.aggregated_data,'{}'),COALESCE(v.report,'null') FROM batches b LEFT JOIN ai_diagnoses d ON d.batch_id=b.id AND d.status='completed' LEFT JOIN version_regression_reports v ON v.batch_id=b.id WHERE b.id=$1`, id).Scan(&report.VehicleID, &report.VIN, &report.Status, &report.TotalFiles, &report.ProcessedFiles, &report.CreatedAt, &report.UpdatedAt, &diagnosisID, &summary, &investigation, &report.VersionRegression)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var structured struct {
		Hypotheses  json.RawMessage `json:"hypotheses"`
		Trace       json.RawMessage `json:"tool_trace"`
		Evidence    json.RawMessage `json:"evidence"`
		Limitations json.RawMessage `json:"limitations"`
	}
	if err = json.Unmarshal(investigation, &structured); err != nil {
		return nil, err
	}
	report.Hypotheses = structured.Hypotheses
	report.InvestigationTrace = structured.Trace
	report.Evidence = structured.Evidence
	report.DiagnosisLimitations = structured.Limitations
	report.DiagnosisResult = summary.String
	if diagnosisID.Valid {
		report.DiagnosisID = &diagnosisID.UUID
	}
	return report, nil
}

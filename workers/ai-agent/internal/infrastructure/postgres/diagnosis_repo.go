package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/lib/pq"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"time"
)

type PostgresDiagnosisRepository struct{ db *sql.DB }

func NewDiagnosisRepository(db *sql.DB) domain.DiagnosisRepository {
	return &PostgresDiagnosisRepository{db}
}
func NewPostgresDiagnoseRepository(db *sql.DB) domain.DiagnosisRepository {
	return NewDiagnosisRepository(db)
}
func (r *PostgresDiagnosisRepository) GetAggregatedData(ctx context.Context, id string) (*domain.AggregatedData, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM batches WHERE id=$1)`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("batch not found: %s", id)
	}
	d := &domain.AggregatedData{StatsGrain: "affected_file", BatchID: id, ErrorCodeStats: map[string]int{}}
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(NULLIF(error_code,''),'UNKNOWN'),COUNT(*) FROM files WHERE batch_id=$1 GROUP BY 1`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var code string
		var count int
		if err = rows.Scan(&code, &count); err != nil {
			rows.Close()
			return nil, err
		}
		d.ErrorCodeStats[code] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var logs sql.NullString
	err = r.db.QueryRowContext(ctx, `SELECT string_agg(log_content,E'\n' ORDER BY timestamp) FROM (SELECT timestamp,log_content FROM logs WHERE batch_id=$1 ORDER BY timestamp DESC LIMIT 1000) recent`, id).Scan(&logs)
	if err != nil {
		return nil, err
	}
	d.RawLogs = logs.String
	d.LogsSummary = truncate(logs.String, 2400)
	var parsedCount int
	if err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parsed_file_data WHERE batch_id=$1`, id).Scan(&parsedCount); err != nil {
		return nil, err
	}
	if parsedCount > 0 {
		d.ErrorCodeStats = map[string]int{}
		records, e := r.db.QueryContext(ctx, `SELECT o->>'error_code',COUNT(*) FROM parsed_file_data p CROSS JOIN LATERAL jsonb_array_elements(p.observations) o WHERE p.batch_id=$1 AND COALESCE(o->>'error_code','')<>'' GROUP BY 1`, id)
		if e != nil {
			return nil, e
		}
		for records.Next() {
			var code string
			var count int
			if e = records.Scan(&code, &count); e != nil {
				records.Close()
				return nil, e
			}
			d.ErrorCodeStats[code] = count
		}
		e = records.Err()
		records.Close()
		if e != nil {
			return nil, e
		}
		d.StatsGrain = "log_occurrence"
		var allLogs int
		if e = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE batch_id=$1`, id).Scan(&allLogs); e != nil {
			return nil, e
		}
		ApplyParsedCoverage(d, allLogs)
	}
	return d, nil
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n*3/5]) + "\n...\n" + string(r[len(r)-n*2/5:])
}

type queryer interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

const diagnosisColumns = `id,batch_id,status,aggregated_data,diagnosis_summary,COALESCE(top_error_codes,'[]'),recommendations,confidence,model_name,COALESCE(total_tokens,0),diagnosed_at,created_at,updated_at`

func find(ctx context.Context, q queryer, where string, arg string) (*domain.Diagnosis, error) {
	d := &domain.Diagnosis{}
	var agg, codes, recs []byte
	err := q.QueryRowContext(ctx, `SELECT `+diagnosisColumns+` FROM ai_diagnoses WHERE `+where+`=$1`, arg).Scan(&d.ID, &d.BatchID, &d.Status, &agg, &d.DiagnosisSummary, &codes, &recs, &d.Confidence, &d.Model, &d.TokensUsed, &d.DiagnosedAt, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(agg, &d.AggregatedData); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(codes, &d.TopErrorCodes); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(recs, &d.Recommendations); err != nil {
		return nil, err
	}
	return d, nil
}
func (r *PostgresDiagnosisRepository) FindByBatchID(ctx context.Context, id string) (*domain.Diagnosis, error) {
	return find(ctx, r.db, "batch_id", id)
}
func (r *PostgresDiagnosisRepository) FindByID(ctx context.Context, id string) (*domain.Diagnosis, error) {
	return find(ctx, r.db, "id", id)
}

type execer interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func save(ctx context.Context, db execer, d *domain.Diagnosis) error {
	if d == nil {
		return fmt.Errorf("diagnosis is nil")
	}
	agg, err := json.Marshal(d.AggregatedData)
	if err != nil {
		return err
	}
	if d.AggregatedData == nil {
		agg = []byte(`{}`)
	}
	codes, _ := json.Marshal(d.TopErrorCodes)
	recs, _ := json.Marshal(d.Recommendations)
	if d.TopErrorCodes == nil {
		codes = []byte(`[]`)
	}
	if d.Recommendations == nil {
		recs = []byte(`[]`)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO ai_diagnoses(id,batch_id,status,aggregated_data,diagnosis_summary,top_error_codes,recommendations,confidence,model_name,total_tokens,diagnosed_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(batch_id) DO UPDATE SET status=EXCLUDED.status,aggregated_data=EXCLUDED.aggregated_data,diagnosis_summary=EXCLUDED.diagnosis_summary,top_error_codes=EXCLUDED.top_error_codes,recommendations=EXCLUDED.recommendations,confidence=EXCLUDED.confidence,model_name=EXCLUDED.model_name,total_tokens=EXCLUDED.total_tokens,diagnosed_at=EXCLUDED.diagnosed_at,updated_at=EXCLUDED.updated_at`, d.ID, d.BatchID, d.Status, agg, d.DiagnosisSummary, codes, recs, d.Confidence, d.Model, d.TokensUsed, d.DiagnosedAt, d.CreatedAt, d.UpdatedAt)
	return err
}
func (r *PostgresDiagnosisRepository) Save(ctx context.Context, d *domain.Diagnosis) error {
	return save(ctx, r.db, d)
}

func (r *PostgresDiagnosisRepository) FindSimilar(ctx context.Context, v []float32, limit int) ([]*domain.Diagnosis, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM ai_diagnoses WHERE embedding IS NOT NULL ORDER BY embedding <=> $1::vector LIMIT $2`, pq.Array(v), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []*domain.Diagnosis{}
	for _, id := range ids {
		d, e := r.FindByID(ctx, id)
		if e != nil {
			return nil, e
		}
		result = append(result, d)
	}
	return result, nil
}

// RecordFailure persists terminal investigation state and its notification atomically.
func (r *PostgresDiagnosisRepository) RecordFailure(ctx context.Context, id string, cause error) error {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return nil
	}
	id = batchID.String()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM batches WHERE id=$1)`, batchID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	// A quarantined failure from an older generation must not overwrite a newer run.
	var originating *domain.ExecutionFailure
	if errors.As(cause, &originating) {
		var generation int64
		e := tx.QueryRowContext(ctx, `SELECT generation FROM investigation_executions WHERE batch_id=$1 FOR UPDATE`, batchID).Scan(&generation)
		if e != nil {
			return e
		}
		if generation != originating.Lease.Generation {
			return nil
		}
	}
	if err = validateExecution(ctx, tx, ctx, id); err != nil {
		return err
	}
	old, err := find(ctx, tx, "batch_id", id)
	if err != nil {
		return err
	}
	if old != nil && old.Status == domain.DiagnosisStatusCompleted {
		return nil
	}
	if err = persistFailure(ctx, tx, batchID, id, old, cause); err != nil {
		return err
	}
	return tx.Commit()
}
func persistFailure(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, id string, old *domain.Diagnosis, cause error) error {
	var err error
	now := time.Now()
	message := core.SafeText(cause.Error(), 1500)
	d := &domain.Diagnosis{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("argus-diagnosis:"+id)), BatchID: batchID, Status: domain.DiagnosisStatusFailed, DiagnosisSummary: "Investigation failed: " + message, AggregatedData: map[string]interface{}{"failure": message}, Model: "failed-investigation", CreatedAt: now, UpdatedAt: now}
	if old != nil {
		d.ID = old.ID
		d.CreatedAt = old.CreatedAt
	}
	if err = save(ctx, tx, d); err != nil {
		return err
	}
	if err = corepg.EnqueueEvent(ctx, tx, core.WireEvent{Type: "DiagnosisFailed", BatchID: batchID, Timestamp: now, Data: map[string]interface{}{"reason": message}}); err != nil {
		return err
	}
	return nil
}

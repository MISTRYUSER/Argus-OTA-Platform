package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"

	"github.com/lib/pq"
)

type PostgresDiagnosisRepository struct {
	db *sql.DB
}

// NewDiagnosisRepository 创建 PostgreSQL Diagnosis Repository
func NewDiagnosisRepository(db *sql.DB) domain.DiagnosisRepository {
	return &PostgresDiagnosisRepository{db: db}
}

func NewPostgresDiagnoseRepository(db *sql.DB) domain.DiagnosisRepository {
	return &PostgresDiagnosisRepository{db: db}
}

func (r *PostgresDiagnosisRepository) GetAggregatedData(ctx context.Context, batchID string) (*domain.AggregatedData, error) {
	// 1. 查询批次基础信息
	batchQuery := `
  		SELECT id, status, total_files, processed_files
  		FROM batches
  		WHERE id = $1
  	`
	var batchID2 string
	var status string
	var totalFiles, processedFiles int
	err := r.db.QueryRowContext(ctx, batchQuery, batchID).Scan(&batchID2, &status, &totalFiles, &processedFiles)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("batch not found: %s", batchID)
		}
		return nil, fmt.Errorf("failed to query batch: %w", err)
	}

	// 2. 聚合错误码统计
	errorCodeQuery := `
	  		SELECT COALESCE(NULLIF(error_code, ''), 'UNKNOWN') AS error_code, COUNT(*) as count
	  		FROM files
	  		WHERE batch_id = $1
	  		GROUP BY COALESCE(NULLIF(error_code, ''), 'UNKNOWN')
	  		ORDER BY count DESC
	  	`
	rows, err := r.db.QueryContext(ctx, errorCodeQuery, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to query error codes: %w", err)
	}
	defer rows.Close()

	errorCodeStats := make(map[string]int)
	for rows.Next() {
		var errorCode string
		var count int
		if err := rows.Scan(&errorCode, &count); err != nil {
			return nil, fmt.Errorf("failed to scan error code: %w", err)
		}
		errorCodeStats[errorCode] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating error code rows: %w", err)
	}

	// 3. 聚合日志（取前 1000 条，避免过多）
	logsQuery := `
  		SELECT string_agg(log_content, E'\n') as logs
  		FROM (
  			SELECT log_content
  			FROM logs
  			WHERE batch_id = $1
  			ORDER BY timestamp DESC
  			LIMIT 1000
  		) subquery
  	`
	var rawLogs sql.NullString
	err = r.db.QueryRowContext(ctx, logsQuery, batchID).Scan(&rawLogs)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}

	// 4. 返回聚合数据
	return &domain.AggregatedData{
		BatchID:        batchID,
		ErrorCodeStats: errorCodeStats,
		RawLogs:        rawLogs.String,
		LogsSummary:    summarizeLogs(rawLogs.String, 2400),
	}, nil
}

func (r *PostgresDiagnosisRepository) Save(ctx context.Context, diagnose *domain.Diagnosis) error {
	query := `
  		INSERT INTO ai_diagnoses (
  			id, batch_id, status, aggregated_data,
  			diagnosis_summary, top_error_codes, recommendations, confidence,
  			embedding, model, tokens_used, diagnosed_at,
  			created_at, updated_at
  		) VALUES (
  			$1, $2, $3, $4,
  			$5, $6, $7, $8,
  			$9, $10, $11, $12,
  			$13, $14
  		)
  		ON CONFLICT (id) DO UPDATE SET
  			status = EXCLUDED.status,
  			aggregated_data = EXCLUDED.aggregated_data,
  			diagnosis_summary = EXCLUDED.diagnosis_summary,
  			top_error_codes = EXCLUDED.top_error_codes,
  			recommendations = EXCLUDED.recommendations,
  			confidence = EXCLUDED.confidence,
  			embedding = EXCLUDED.embedding,
  			model = EXCLUDED.model,
  			tokens_used = EXCLUDED.tokens_used,
  			diagnosed_at = EXCLUDED.diagnosed_at,
  			updated_at = EXCLUDED.updated_at
  	`

	aggregatedJSON, err := json.Marshal(diagnose.AggregatedData)
	if err != nil {
		return fmt.Errorf("failed to marshal aggregated_data: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		diagnose.ID, diagnose.BatchID, diagnose.Status, aggregatedJSON,
		diagnose.DiagnosisSummary, pq.Array(diagnose.TopErrorCodes), pq.Array(diagnose.Recommendations), diagnose.Confidence,
		pq.Array(diagnose.Embedding), diagnose.Model, diagnose.TokensUsed, diagnose.DiagnosedAt,
		diagnose.CreatedAt, diagnose.UpdatedAt,
	)

	return err
}

func (r *PostgresDiagnosisRepository) FindByBatchID(ctx context.Context, batchID string) (*domain.Diagnosis, error) {
	query := `
	  		SELECT id, batch_id, status, aggregated_data,
	  			   diagnosis_summary, top_error_codes, recommendations, confidence,
	  			   embedding, model, tokens_used, diagnosed_at,
	  			   created_at, updated_at
	  		FROM ai_diagnoses
	  		WHERE batch_id = $1
			ORDER BY updated_at DESC
			LIMIT 1
	  	`

	row := r.db.QueryRowContext(ctx, query, batchID)

	var diagnose domain.Diagnosis
	var aggregatedJSON []byte

	err := row.Scan(
		&diagnose.ID, &diagnose.BatchID, &diagnose.Status, &aggregatedJSON,
		&diagnose.DiagnosisSummary, pq.Array(&diagnose.TopErrorCodes), pq.Array(&diagnose.Recommendations), &diagnose.Confidence,
		pq.Array(&diagnose.Embedding), &diagnose.Model, &diagnose.TokensUsed, &diagnose.DiagnosedAt,
		&diagnose.CreatedAt, &diagnose.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(aggregatedJSON, &diagnose.AggregatedData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal aggregated_data: %w", err)
	}

	return &diagnose, err
}

func (r *PostgresDiagnosisRepository) FindByID(ctx context.Context, id string) (*domain.Diagnosis, error) {
	query := `
		SELECT id, batch_id, status, aggregated_data,
			   diagnosis_summary, top_error_codes, recommendations, confidence,
			   embedding, model, tokens_used, diagnosed_at,
			   created_at, updated_at
		FROM ai_diagnoses
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, id)

	var diagnose domain.Diagnosis
	var aggregatedJSON []byte

	err := row.Scan(
		&diagnose.ID, &diagnose.BatchID, &diagnose.Status, &aggregatedJSON,
		&diagnose.DiagnosisSummary, pq.Array(&diagnose.TopErrorCodes), pq.Array(&diagnose.Recommendations), &diagnose.Confidence,
		pq.Array(&diagnose.Embedding), &diagnose.Model, &diagnose.TokensUsed, &diagnose.DiagnosedAt,
		&diagnose.CreatedAt, &diagnose.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(aggregatedJSON, &diagnose.AggregatedData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal aggregated_data: %w", err)
	}

	return &diagnose, nil
}

// FindSimilar 查找相似诊断（RAG，基于向量相似度）
func (r *PostgresDiagnosisRepository) FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*domain.Diagnosis, error) {
	query := `
		SELECT id, batch_id, diagnosis_summary, confidence,
			   1 - (embedding <=> $1::vector) as similarity
		FROM ai_diagnoses
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, pq.Array(embedding), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var diagnoses []*domain.Diagnosis
	for rows.Next() {
		var diagnose domain.Diagnosis
		var similarity float64
		if err := rows.Scan(&diagnose.ID, &diagnose.BatchID, &diagnose.DiagnosisSummary, &diagnose.Confidence, &similarity); err != nil {
			return nil, err
		}
		diagnoses = append(diagnoses, &diagnose)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return diagnoses, nil
}

func summarizeLogs(raw string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= maxLen {
		return raw
	}

	lines := splitNonEmptyLines(raw)
	if len(lines) == 0 {
		return ""
	}

	errorCount, warnCount, infoCount, otherCount := summarizeLevels(lines)
	summary := fmt.Sprintf(
		"total_lines=%d; ERROR=%d; WARN=%d; INFO=%d; OTHER=%d; first=%q; last=%q",
		len(lines), errorCount, warnCount, infoCount, otherCount, clipLine(lines[0], 100), clipLine(lines[len(lines)-1], 100),
	)

	if len(summary) <= maxLen {
		return summary
	}
	return truncateMiddle(summary, maxLen)
}

func splitNonEmptyLines(raw string) []string {
	parts := strings.Split(raw, "\n")
	lines := make([]string, 0, len(parts))
	for _, line := range parts {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func summarizeLevels(lines []string) (errorCount, warnCount, infoCount, otherCount int) {
	for _, line := range lines {
		upper := strings.ToUpper(line)
		switch {
		case strings.Contains(upper, "ERROR"):
			errorCount++
		case strings.Contains(upper, "WARN"):
			warnCount++
		case strings.Contains(upper, "INFO"):
			infoCount++
		default:
			otherCount++
		}
	}
	return
}

func clipLine(line string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(line) <= maxLen {
		return line
	}
	if maxLen <= 3 {
		return line[:maxLen]
	}
	return line[:maxLen-3] + "..."
}

func truncateMiddle(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	frontLen := maxLen * 3 / 5
	backLen := maxLen - frontLen
	if frontLen <= 0 || backLen <= 0 {
		return value[:maxLen]
	}
	return value[:frontLen] + value[len(value)-backLen:]
}

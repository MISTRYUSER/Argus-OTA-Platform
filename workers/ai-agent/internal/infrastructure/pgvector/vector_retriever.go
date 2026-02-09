package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

type PgvectorRetriever struct {
	db         *sql.DB
	embedModel embedding.Embedder
}

func NewPgvectorRetriever(db *sql.DB, embedModel embedding.Embedder) (*PgvectorRetriever, error) {
	if db == nil {
		return nil, fmt.Errorf("db connect is nil")
	}
	if embedModel == nil {
		return nil, fmt.Errorf("embedding model is nil")
	}
	return &PgvectorRetriever{
		db:         db,
		embedModel: embedModel,
	}, nil
}

// Search 混合检索（错误码过滤 + 向量排序）
func (r *PgvectorRetriever) Search(ctx context.Context, params domain.SearchParams) ([]domain.SimilarCase, error) {
	if params.EmbeddingText == "" {
		return nil, fmt.Errorf("embedding text is empty")
	}
	if r.db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if r.embedModel == nil {
		return nil, fmt.Errorf("embedding model is nil")
	}

	queryEmbeddings, err := r.embedModel.EmbedStrings(ctx, []string{params.EmbeddingText})
	if err != nil {
		return nil, err
	}

	if len(queryEmbeddings) == 0 {
		return nil, fmt.Errorf("no embeddings generated")
	}

	queryVector := queryEmbeddings[0]
	vectorLiteral, err := toVectorLiteral(queryVector)
	if err != nil {
		return nil, err
	}

	// pgvector query（混合检索）
	sql := `
		SELECT
			id,
			batch_id,
			diagnosis_summary,
			confidence,
			1 - (embedding <=> $1::vector) AS similarity
		FROM ai_diagnoses
		WHERE embedding IS NOT NULL
			AND ($2::text[] IS NULL OR top_error_codes && $2::text[])
		ORDER BY embedding <=> $1::vector
		LIMIT $3;
	`

	// prepare for pgvector query
	topK := params.TopK
	if topK <= 0 {
		topK = 5
	}

	rows, err := r.db.QueryContext(ctx, sql, vectorLiteral, buildErrorCodesParam(params.ErrorCodes), topK)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar cases: %w", err)
	}
	defer rows.Close()

	// parse query result
	cases := []domain.SimilarCase{}
	for rows.Next() {
		var c domain.SimilarCase
		var similarity float64

		err := rows.Scan(
			&c.DiagnosisID,
			&c.BatchID,
			&c.Summary,
			&c.Confidence,
			&similarity,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		c.Similarity = similarity
		c.MatchedReason = fmt.Sprintf("相似度: %.2f", similarity)

		cases = append(cases, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return cases, nil
}

// Retrieve 检索相似案例（向后兼容）
func (r *PgvectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]domain.SimilarCase, error) {
	return r.Search(ctx, domain.SearchParams{
		EmbeddingText: query,
		ErrorCodes:    nil,
		TopK:          topK,
	})
}

// Index 索引新的诊断案例（增量更新）
func (r *PgvectorRetriever) Index(ctx context.Context, diagnosis *domain.Diagnosis) error {
	if diagnosis == nil {
		return fmt.Errorf("diagnosis is nil")
	}

	if diagnosis.DiagnosisSummary == "" {
		return fmt.Errorf("diagnosis summary is empty")
	}

	embeddings, err := r.embedModel.EmbedStrings(ctx, []string{diagnosis.DiagnosisSummary})
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	if len(embeddings) == 0 {
		return fmt.Errorf("no embeddings generated")
	}

	embedding := embeddings[0]
	vectorLiteral, err := toVectorLiteral(embedding)
	if err != nil {
		return err
	}

	sql := `
		UPDATE ai_diagnoses
		SET embedding = $1,
			updated_at = NOW()
		WHERE id = $2;
	`

	_, err = r.db.ExecContext(ctx, sql, vectorLiteral, diagnosis.ID)
	if err != nil {
		return fmt.Errorf("failed to update embedding: %w", err)
	}

	return nil
}

func buildErrorCodesParam(errorCodes []string) interface{} {
	if len(errorCodes) == 0 {
		return nil
	}
	return pq.Array(errorCodes)
}

func toVectorLiteral(values []float64) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("vector is empty")
	}

	var b strings.Builder
	b.WriteByte('[')
	for i, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("vector contains invalid value at %d", i)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	}
	b.WriteByte(']')
	return b.String(), nil
}

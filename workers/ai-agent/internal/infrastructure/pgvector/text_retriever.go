package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// NewTextRetriever enables case lookup when the chat provider has no embedding API.
func NewTextRetriever(db *sql.DB) *PgvectorRetriever { return &PgvectorRetriever{db: db} }
func (r *PgvectorRetriever) searchText(ctx context.Context, p domain.SearchParams) ([]domain.SimilarCase, error) {
	limit := p.TopK
	if limit < 1 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,symptom_text,solution_text FROM knowledge_base WHERE ($1::text[] IS NULL OR error_code=ANY($1)) AND ($1::text[] IS NOT NULL OR symptom_text ILIKE '%'||$2||'%' OR solution_text ILIKE '%'||$2||'%') ORDER BY (CASE WHEN $2 <> '' AND (symptom_text ILIKE '%'||$2||'%' OR solution_text ILIKE '%'||$2||'%') THEN 1 ELSE 0 END) DESC, ts_rank(to_tsvector('simple',symptom_text||' '||solution_text),plainto_tsquery('simple',$2)) DESC,id LIMIT $3`, buildErrorCodesParam(p.ErrorCodes), p.EmbeddingText, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.SimilarCase{}
	for rows.Next() {
		var id int64
		var symptom, solution string
		if err = rows.Scan(&id, &symptom, &solution); err != nil {
			return nil, err
		}
		result = append(result, domain.SimilarCase{DiagnosisID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("knowledge-base:%d", id))), Summary: symptom + "; " + solution, MatchedReason: fmt.Sprintf("knowledge_base:%d (structured/text lookup)", id)})
	}
	return result, rows.Err()
}

package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"
)

// PromoteCase is an explicit operator action after independently resolving a
// historical incident. Model output never calls this method or self-verifies.
func (r *PgvectorRetriever) PromoteCase(ctx context.Context, id uuid.UUID, cause, module, exception, reviewer string) error {
	if id == uuid.Nil || strings.TrimSpace(cause) == "" || strings.TrimSpace(module) == "" || strings.TrimSpace(reviewer) == "" || len(cause) > 128 || len(module) > 128 || len(exception) > 128 || len(reviewer) > 128 {
		return fmt.Errorf("case ID, cause, module and reviewer required; labels max 128 bytes")
	}
	if r.db == nil || r.embedModel == nil {
		return fmt.Errorf("vector database and embedding model required")
	}
	var summary string
	if err := r.db.QueryRowContext(ctx, `SELECT diagnosis_summary FROM ai_diagnoses WHERE id=$1 AND status='completed'`, id).Scan(&summary); err != nil {
		return err
	}
	if strings.TrimSpace(summary) == "" || len(summary) > 4000 {
		return fmt.Errorf("case summary empty or oversized")
	}
	vectors, err := r.embedModel.EmbedStrings(ctx, []string{summary})
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("one embedding required")
	}
	vector, err := toVectorLiteral(vectors[0])
	if err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]interface{}{"case_verified": true, "root_cause_id": cause, "case_module": module, "case_exception": exception, "case_reviewer": reviewer, "case_verified_at": time.Now().UTC().Format(time.RFC3339)})
	result, err := r.db.ExecContext(ctx, `UPDATE ai_diagnoses SET embedding=$1::vector,aggregated_data=COALESCE(aggregated_data,'{}'::jsonb)||$2::jsonb,updated_at=NOW() WHERE id=$3 AND status='completed' AND diagnosis_summary=$4`, vector, metadata, id, summary)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("case changed during verification; review again")
	}
	return nil
}

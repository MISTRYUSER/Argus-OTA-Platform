package pgvector

import (
	"context"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"strings"
	"testing"
)

func TestTextQueryChangesRankingWithSameErrorCode(t *testing.T) {
	db := testutil.OpenDB(t)
	v := "[" + strings.TrimSuffix(strings.Repeat("0,", 1536), ",") + "]"
	for _, text := range []string{"SYNTHETIC firmware regression", "SYNTHETIC fan stopped"} {
		_, err := db.Exec(`INSERT INTO knowledge_base(error_code,vehicle_platform,severity,symptom_text,solution_text,embedding) VALUES('TEST_REVERSE','synthetic','error',$1,'inspect',$2::vector)`, text, v)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := NewTextRetriever(db)
	for _, q := range []string{"firmware", "fan stopped"} {
		got, err := r.Search(context.Background(), domain.SearchParams{ErrorCodes: []string{"TEST_REVERSE"}, EmbeddingText: q, TopK: 1})
		if err != nil || len(got) != 1 || !strings.Contains(got[0].Summary, q) {
			t.Fatalf("query=%s got=%v err=%v", q, got, err)
		}
	}
}

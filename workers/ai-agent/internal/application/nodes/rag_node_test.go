package nodes

import (
	"context"
	"testing"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

type okRetriever struct{}

func (o *okRetriever) Search(ctx context.Context, params domain.SearchParams) ([]domain.SimilarCase, error) {
	return []domain.SimilarCase{{Summary: "ok"}}, nil
}

func (o *okRetriever) Retrieve(ctx context.Context, query string, topK int) ([]domain.SimilarCase, error) {
	return nil, nil
}

func (o *okRetriever) Index(ctx context.Context, diagnosis *domain.Diagnosis) error {
	return nil
}

func TestRAGNode_NilRetriever_DegradesWithoutError(t *testing.T) {
	node := NewRAGNode(nil)
	input := &domain.DiagnosisContext{
		BatchID: "b1",
		AggregatedData: &domain.AggregatedData{
			LogsSummary: "test logs",
		},
	}

	out, err := node.Transform(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil output")
	}
	if !out.RAGUnavailable {
		t.Fatalf("expected RAGUnavailable=true when retriever is nil")
	}
}

func TestRAGNode_NilAggregatedData_DoesNotPanic(t *testing.T) {
	node := NewRAGNode(&okRetriever{})
	input := &domain.DiagnosisContext{
		BatchID: "b1",
	}

	_, err := node.Transform(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error for nil aggregated data, got %v", err)
	}
}

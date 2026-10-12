package supervisor

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"net/http"
	"net/http/httptest"
	"testing"
)

type repoFixture struct{ domain.DiagnosisRepository }

func (repoFixture) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{RawLogs: "CAN connector timeout", LogsSummary: "CAN connector timeout", ErrorCodeStats: map[string]int{"E001": 1}}, nil
}
func TestSupervisorReturnsParsedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"{\"root_cause\":\"Connector loose\",\"suggestions\":[\"Inspect connector\"],\"severity\":\"high\",\"confidence\":0.85}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	graph, err := NewSupervisorGraph(repoFixture{}, nil, &llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := graph.Run(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if got.RootCause != "Connector loose" {
		t.Fatal(got.RootCause)
	}
}
func TestSupervisorFailureIsNotConvertedToSuccess(t *testing.T) {
	s := domain.NewDiagnosisContext("")
	s.LastWorker = "guard"
	s.ProcessingStatus = domain.StatusFailed
	s.ErrorMessage = "blocked"
	chain := compose.NewChain[*domain.DiagnosisContext, *domain.DiagnosisContext]()
	chain.AppendLambda(NewSupervisorRunnable(NewSupervisorAgent()))
	runner, err := chain.Compile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Invoke(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if s.ProcessingStatus != domain.StatusFailed {
		t.Fatalf("status=%s", s.ProcessingStatus)
	}
}

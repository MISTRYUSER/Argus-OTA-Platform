package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistorySearchGuard(t *testing.T) {
	s := &agentState{}
	if err := allowHistorySearch(s, `{"query":"fan","error_codes":["E001"]}`); err != nil {
		t.Fatal(err)
	}
	s.observations = append(s.observations, domain.ToolObservation{Tool: "search_history", Arguments: json.RawMessage(`{"error_codes":["E001"],"query":" FAN "}`)})
	if allowHistorySearch(s, `{"query":"fan","error_codes":["E001"]}`) == nil {
		t.Fatal("duplicate allowed")
	}
	for _, q := range []string{"cpu", "firmware"} {
		s.observations = append(s.observations, domain.ToolObservation{Tool: "search_history", Arguments: json.RawMessage(`{"query":"` + q + `"}`)})
	}
	if allowHistorySearch(s, `{"query":"voltage"}`) == nil {
		t.Fatal("fourth search allowed")
	}
}

type reversalRepo struct{ repositoryFixture }

func (reversalRepo) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{RawLogs: "SYNTHETIC E001 CPU temperature=95C; fan speed=0RPM\nSYNTHETIC firmware unchanged; voltage stable", ErrorCodeStats: map[string]int{"E001": 1}}, nil
}

type reversalRetriever struct {
	domain.VectorRetriever
	queries []string
}

func (r *reversalRetriever) Search(_ context.Context, p domain.SearchParams) ([]domain.SimilarCase, error) {
	r.queries = append(r.queries, p.EmbeddingText)
	summary := "SYNTHETIC old case: firmware regression"
	if p.EmbeddingText == "fan stopped" {
		summary = "SYNTHETIC matching case: stopped fan causes thermal fault"
	}
	return []domain.SimilarCase{{DiagnosisID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(summary)), Summary: summary, MatchedReason: "synthetic fixture"}}, nil
}
func TestAgentRefutesInitialHypothesisAndRefinesRetrieval(t *testing.T) {
	initial := `{"hypotheses":[{"id":"H1","description":"firmware regression","status":"open","next_check":"read current logs"}]}`
	revised := `{"hypotheses":[{"id":"H1","description":"firmware regression","status":"refuted","refutes":["log:2"],"next_check":"none"},{"id":"H2","description":"fan stopped","status":"supported","supports":["log:1"],"next_check":"physical inspection"}]}`
	call := func(name, args string) map[string]interface{} {
		return map[string]interface{}{"id": uuid.NewString(), "type": "function", "function": map[string]string{"name": name, "arguments": args}}
	}
	steps := [][]map[string]interface{}{{call("search_history", `{"query":"firmware","error_codes":["E001"]}`), call("update_investigation_plan", initial)}, {call("read_logs", `{}`), call("error_statistics", `{}`)}, {call("search_history", `{"query":"fan stopped","error_codes":["E001"]}`), call("update_investigation_plan", revised)}}
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		m := map[string]interface{}{"role": "assistant", "content": `{"hypothesis_id":"H2","root_cause":"fan stopped","suggestions":["inspect fan"],"severity":"high","confidence":0.8,"evidence_ids":["log:1","log:2"],"limitations":["synthetic fixture"]}`}
		if n <= len(steps) {
			m["content"] = ""
			m["tool_calls"] = steps[n-1]
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "message": m}}, "usage": map[string]int{"total_tokens": 100}})
	}))
	defer server.Close()
	chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	retriever := &reversalRetriever{}
	a, err := NewAgent(reversalRepo{}, retriever, chat, Options{MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Run(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if result.HypothesisID != "H2" || result.Hypotheses[0].Status != "refuted" || len(retriever.queries) != 2 || result.TokensUsed != 400 {
		t.Fatalf("reversal missing: %+v queries=%v", result, retriever.queries)
	}
	if len(result.ToolTrace) != 6 {
		t.Fatal("initial/revised plans not preserved")
	}
}

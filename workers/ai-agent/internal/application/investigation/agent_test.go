package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type repositoryFixture struct{ domain.DiagnosisRepository }

func (repositoryFixture) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{RawLogs: "CAN connector timeout", ErrorCodeStats: map[string]int{"E001": 1}}, nil
}
func makeAgent(t *testing.T, final string, max int, history ...bool) (*Agent, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		message := map[string]interface{}{"role": "assistant", "content": final}
		if calls == 1 {
			message = map[string]interface{}{"role": "assistant", "content": "", "tool_calls": []interface{}{map[string]interface{}{"id": "stats", "type": "function", "function": map[string]string{"name": "error_statistics", "arguments": "{}"}}, map[string]interface{}{"id": "logs", "type": "function", "function": map[string]string{"name": "read_logs", "arguments": `{"query":"CAN"}`}}}}
		}
		if calls == 1 && len(history) > 0 && history[0] {
			message["tool_calls"] = append(message["tool_calls"].([]interface{}), map[string]interface{}{"id": "history", "type": "function", "function": map[string]string{"name": "search_history", "arguments": "{}"}})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"id": "fixture", "choices": []interface{}{map[string]interface{}{"index": 0, "message": message, "finish_reason": "stop"}}})
	}))
	t.Cleanup(server.Close)
	model, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(repositoryFixture{}, nil, model, Options{MaxSteps: max})
	if err != nil {
		t.Fatal(err)
	}
	return agent, &calls
}
func TestAgentUsesModelSelectedToolsAndEvidence(t *testing.T) {
	agent, calls := makeAgent(t, `{"root_cause":"Connector loose","suggestions":["Inspect connector"],"severity":"high","confidence":0.85,"evidence_ids":["log:1"],"limitations":["Synthetic fixture"]}`, 4)
	result, err := agent.Run(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if result.RootCause != "Connector loose" || len(result.ToolTrace) != 2 || len(result.Evidence) != 1 || result.Evidence[0].ID != "log:1" {
		t.Fatalf("result=%+v", result)
	}
	if *calls != 2 {
		t.Fatalf("model calls=%d", *calls)
	}
}
func TestAgentRejectsInventedEvidence(t *testing.T) {
	agent, _ := makeAgent(t, `{"root_cause":"Connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["invented"]}`, 4)
	if _, err := agent.Run(context.Background(), uuid.NewString()); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("expected evidence rejection, got %v", err)
	}
}
func TestAgentRejectsMalformedDiagnosis(t *testing.T) {
	agent, _ := makeAgent(t, `{"root_cause":"Connector loose","severity":"high","evidence_ids":["log:1"]}`, 4)
	if _, err := agent.Run(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("missing confidence was accepted")
	}
}
func TestAgentStopsAtStepBudget(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"}}]}`)
	}))
	defer server.Close()
	model, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(repositoryFixture{}, nil, model, Options{MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agent.Run(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("unbounded/incomplete investigation accepted")
	}
	if count != 3 {
		t.Fatalf("model calls=%d", count)
	}
}

func TestRetrievalFailureCannotDisappearFromReport(t *testing.T) {
	agent, _ := makeAgent(t, `{"root_cause":"Connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`, 4, true)
	result, err := agent.Run(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Limitations) == 0 {
		t.Fatal("retrieval unavailable but report omitted limitation")
	}
}

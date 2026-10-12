package investigation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type processStore struct {
	repositoryFixture
	path  string
	saves int
}

func (s *processStore) LoadInvestigationCheckpoint(context.Context, string) ([]byte, error) {
	b, e := os.ReadFile(s.path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	return b, e
}
func (s *processStore) SaveInvestigationCheckpoint(_ context.Context, _ string, b []byte) error {
	if e := os.WriteFile(s.path+".tmp", b, 0600); e != nil {
		return e
	}
	if e := os.Rename(s.path+".tmp", s.path); e != nil {
		return e
	}
	s.saves++
	if os.Getenv("ARGUS_CHILD_CRASH") == "1" && s.saves == 3 {
		os.Exit(77)
	}
	return nil
}
func TestRecoveryChild(t *testing.T) {
	if os.Getenv("ARGUS_RECOVERY_CHILD") != "1" {
		return
	}
	chat, e := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: os.Getenv("ARGUS_CHILD_URL")})
	if e != nil {
		t.Fatal(e)
	}
	store := &processStore{path: os.Getenv("ARGUS_CHILD_CHECKPOINT")}
	a, e := NewAgent(store, nil, chat, Options{MaxSteps: 4})
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.Run(context.Background(), os.Getenv("ARGUS_CHILD_BATCH"))
	if e != nil {
		t.Fatal(e)
	}
	if len(result.ToolTrace) != 3 || result.TokensUsed != 200 || result.ModelSteps != 2 || result.ReservedTokens <= 0 {
		t.Fatalf("restored counters/trace incorrect: %+v", result)
	}
	b, _ := json.Marshal(result)
	if e = os.WriteFile(store.path+".result", b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestProcessExitResumesCommittedPrefix(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		message := map[string]interface{}{"role": "assistant", "content": `{"hypothesis_id":"H1","root_cause":"connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`}
		if calls == 1 {
			message["content"] = ""
			message["tool_calls"] = []interface{}{map[string]interface{}{"id": "stats", "type": "function", "function": map[string]string{"name": "error_statistics", "arguments": "{}"}}, map[string]interface{}{"id": "logs", "type": "function", "function": map[string]string{"name": "read_logs", "arguments": "{}"}}, map[string]interface{}{"id": "plan", "type": "function", "function": map[string]string{"name": "update_investigation_plan", "arguments": `{"hypotheses":[{"id":"H1","description":"connector loose","status":"supported","supports":["log:1"],"next_check":"inspect"}]}`}}}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "message": message}}, "usage": map[string]int{"total_tokens": 100}})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	id := uuid.NewString()
	child := func(crash string) error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryChild$")
		cmd.Env = append(os.Environ(), "ARGUS_RECOVERY_CHILD=1", "ARGUS_CHILD_CRASH="+crash, "ARGUS_CHILD_URL="+server.URL, "ARGUS_CHILD_BATCH="+id, "ARGUS_CHILD_CHECKPOINT="+path)
		out, e := cmd.CombinedOutput()
		if e != nil && crash != "1" {
			t.Log(string(out))
		}
		return e
	}
	err := child("1")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 77 {
		t.Fatalf("child did not terminate at checkpoint: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var before checkpoint
	if e := json.Unmarshal(raw, &before); e != nil {
		t.Fatal(e)
	}
	if before.NextTool != 1 || before.Step != 1 || before.Tokens != 100 || before.ReservedTokens <= 0 {
		t.Fatalf("bad crash snapshot: %+v", before)
	}
	if e := child("0"); e != nil {
		t.Fatal(e)
	}
	raw, _ = os.ReadFile(path)
	var after checkpoint
	json.Unmarshal(raw, &after)
	if calls != 2 || after.Step != 2 || after.ReservedTokens <= before.ReservedTokens || len(after.Observations) != 3 {
		t.Fatalf("prefix/counters replayed: calls=%d before=%d after=%d", calls, before.ReservedTokens, after.ReservedTokens)
	}
	if e := child("0"); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("completed checkpoint called model")
	}
	t.Logf("process recovery: actual exit=77, model_calls=%d, saved_tool_prefix=1, steps=%d tokens=%d reserved=%d", calls, after.Step, after.Tokens, after.ReservedTokens)
}
func TestBudgetRejectsBeforeModelDispatch(t *testing.T) {
	a, calls := makeAgent(t, `{}`, 4)
	a.options.MaxReservedTokens = 1
	if _, e := a.Run(context.Background(), uuid.NewString()); !errors.Is(e, domain.ErrInvestigationBudget) {
		t.Fatalf("expected budget error: %v", e)
	}
	if *calls != 0 {
		t.Fatal("dispatched despite budget")
	}
}
func TestCheckpointRejectsChangedBudgetPolicy(t *testing.T) {
	base, _ := makeAgent(t, `{"hypothesis_id":"H1","root_cause":"connector","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`, 4)
	store := &checkpointFixture{}
	a, _ := NewAgent(store, nil, base.model, Options{MaxSteps: 4})
	id := uuid.NewString()
	if _, e := a.Run(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	a.options.MaxReservedTokens++
	if _, e := a.Run(context.Background(), id); e == nil || !strings.Contains(e.Error(), "mismatch") {
		t.Fatalf("policy changed silently: %v", e)
	}
}
func TestAdmissionRejectionDoesNotConsumeBudget(t *testing.T) {
	base, _ := makeAgent(t, `{}`, 4)
	store := &checkpointFixture{}
	a, _ := NewAgent(store, nil, &busyModel{ChatModel: base.model}, Options{MaxSteps: 4})
	_, e := a.Run(context.Background(), uuid.NewString())
	if !errors.Is(e, domain.ErrModelQueueFull) {
		t.Fatal(e)
	}
	var c checkpoint
	json.Unmarshal(store.data, &c)
	if c.Step != 0 || c.ReservedTokens != 0 {
		t.Fatalf("queue rejection consumed budget: %+v", c)
	}
}

type busyModel struct{ model.ChatModel }

func (*busyModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, domain.ErrModelQueueFull
}
func TestBudgetFinalizationDisablesFurtherTools(t *testing.T) {
	m := &finalizingModel{}
	a := &Agent{repo: repositoryFixture{}, model: m, options: Options{MaxSteps: 2, MaxContextChars: 24000, MaxReservedTokens: 1000000}}
	s := &agentState{step: 1, messages: []*schema.Message{schema.SystemMessage("system"), schema.UserMessage("task")}, used: map[string]bool{"read_logs": true, "error_statistics": true}, hypotheses: []domain.Hypothesis{{ID: "H1", Status: "open"}}, limitations: map[string]string{}}
	if _, e := a.plan(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if !m.forbidden || s.route != "finish" || s.limitations["budget"] == "" {
		t.Fatal("agent kept gathering tools at final budget")
	}
}

type finalizingModel struct {
	model.ChatModel
	forbidden bool
}

func (m *finalizingModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	o := model.GetCommonOptions(nil, opts...)
	m.forbidden = o.ToolChoice != nil && *o.ToolChoice == schema.ToolChoiceForbidden
	return schema.AssistantMessage(`{}`, nil), nil
}
func TestPlannerCannotSkipPlanAfterEvidenceGathering(t *testing.T) {
	m := &planRequiredModel{}
	tools, _ := boundTools(repositoryFixture{}, nil, "fixture")
	planning, _ := planTool(&agentState{evidence: map[string]domain.EvidenceItem{}})
	tools["update_investigation_plan"] = planning
	a := &Agent{repo: repositoryFixture{}, model: m, options: Options{MaxSteps: 8, MaxContextChars: 24000, MaxReservedTokens: 1000000}}
	s := &agentState{tools: tools, step: 1, messages: []*schema.Message{schema.SystemMessage("system"), schema.UserMessage("task")}, used: map[string]bool{"read_logs": true, "error_statistics": true}, limitations: map[string]string{}}
	if _, e := a.plan(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if !m.forced {
		t.Fatal("planner skipped explicit hypothesis after gathering evidence")
	}
}

type planRequiredModel struct {
	model.ChatModel
	forced bool
}

func (m *planRequiredModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	o := model.GetCommonOptions(nil, opts...)
	m.forced = o.ToolChoice != nil && *o.ToolChoice == schema.ToolChoiceForced && len(o.AllowedToolNames) == 1 && o.AllowedToolNames[0] == "update_investigation_plan"
	return schema.AssistantMessage(`{}`, nil), nil
}

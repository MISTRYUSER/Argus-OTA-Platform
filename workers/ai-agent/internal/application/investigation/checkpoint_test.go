package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"strings"
	"testing"
)

type checkpointFixture struct {
	repositoryFixture
	data           []byte
	saves, crashAt int
}

func (s *checkpointFixture) LoadInvestigationCheckpoint(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.data...), nil
}
func (s *checkpointFixture) SaveInvestigationCheckpoint(_ context.Context, _ string, b []byte) error {
	s.data = append([]byte(nil), b...)
	s.saves++
	if s.saves == s.crashAt {
		return fmt.Errorf("simulated crash after committed checkpoint")
	}
	return nil
}
func TestResumeUsesCommittedToolPrefix(t *testing.T) {
	base, calls := makeAgent(t, `{"hypothesis_id":"H1","root_cause":"Connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`, 4)
	store := &checkpointFixture{crashAt: 3}
	agent, err := NewAgent(store, nil, base.model, Options{MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = agent.Run(context.Background(), id); err == nil {
		t.Fatal("expected injected checkpoint crash")
	}
	store.crashAt = 0
	resumed, err := NewAgent(store, nil, base.model, Options{MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	result, err := resumed.Run(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolTrace) != 3 || *calls != 2 {
		t.Fatalf("trace=%d model calls=%d; prefix replayed", len(result.ToolTrace), *calls)
	}
	if _, err = resumed.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatal("completed checkpoint generated again")
	}
}
func TestCheckpointCannotCrossBatch(t *testing.T) {
	base, _ := makeAgent(t, `{"hypothesis_id":"H1","root_cause":"Connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`, 4)
	store := &checkpointFixture{}
	agent, _ := NewAgent(store, nil, base.model, Options{MaxSteps: 4})
	if _, err := agent.Run(context.Background(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Run(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("cross-batch snapshot accepted")
	}
}

func TestCompactionPreservesFullArtifactAndEvidence(t *testing.T) {
	s := &agentState{batchID: uuid.NewString(), artifacts: map[string]json.RawMessage{}, toolArtifacts: map[string]string{}, evidence: map[string]domain.EvidenceItem{}}
	agent := &Agent{repo: repositoryFixture{}, options: Options{MaxContextChars: 2000}}
	body, _ := json.Marshal(toolOutput{Evidence: []domain.EvidenceItem{{ID: "log:1", Source: "fixture", Content: strings.Repeat("evidence", 500)}}})
	id, err := agent.archive(context.Background(), s, string(body))
	if err != nil {
		t.Fatal(err)
	}
	s.toolArtifacts["call"] = id
	s.messages = []*schema.Message{schema.ToolMessage(string(body), "call", schema.WithToolName("read_logs"))}
	if err = agent.compactContext(s); err != nil {
		t.Fatal(err)
	}
	if s.compactions != 1 || len(s.artifacts[id]) != len(body) || !strings.Contains(s.messages[0].Content, "log:1") {
		t.Fatal("compaction lost full output/reference")
	}
	tool, _ := agent.artifactTool(s)
	output, err := tool.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":"%s","limit":5000}`, id))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Content string `json:"content"`
	}
	json.Unmarshal([]byte(output), &page)
	if len([]rune(page.Content)) > 2000 {
		t.Fatal("unbounded artifact page")
	}
}
func TestCompactionArchivesOldAssistantToolGroups(t *testing.T) {
	s := &agentState{batchID: uuid.NewString(), artifacts: map[string]json.RawMessage{}, toolArtifacts: map[string]string{}, evidence: map[string]domain.EvidenceItem{"log:1": {ID: "log:1", Content: "evidence"}}, hypotheses: []domain.Hypothesis{{ID: "H1", Description: "fault", Status: "open"}}}
	s.messages = []*schema.Message{schema.SystemMessage("system"), schema.UserMessage("task")}
	for i := 0; i < 8; i++ {
		s.messages = append(s.messages, &schema.Message{Role: schema.Assistant, Content: strings.Repeat("past reasoning ", 300), ToolCalls: []schema.ToolCall{{ID: fmt.Sprint(i), Function: schema.FunctionCall{Name: "read_logs", Arguments: `{}`}}}}, schema.ToolMessage(`{"evidence":[]}`, fmt.Sprint(i), schema.WithToolName("read_logs")))
	}
	a := &Agent{repo: repositoryFixture{}, options: Options{MaxContextChars: 24000}}
	if e := a.compactContext(s); e != nil {
		t.Fatal(e)
	}
	if len(s.messages) >= 10 || len(s.artifacts) == 0 {
		t.Fatal("old model history not archived")
	}
	if s.messages[len(s.messages)-2].ToolCalls[0].ID != "7" {
		t.Fatal("latest tool group lost")
	}
}

func TestResumeRejectsChangedTelemetryPolicyBeforeModelCall(t *testing.T) {
	base, calls := makeAgent(t, `{"hypothesis_id":"H1","root_cause":"Connector loose","suggestions":[],"severity":"high","confidence":0.8,"evidence_ids":["log:1"]}`, 4)
	store := &checkpointFixture{crashAt: 3}
	first, err := NewAgent(store, nil, base.model, Options{MaxSteps: 4, Telemetry: &signalFixture{}, TelemetryPolicy: "trusted-scope-v1"})
	if err != nil {
		t.Fatal(err)
	}
	batch := uuid.NewString()
	if _, err = first.Run(context.Background(), batch); err == nil {
		t.Fatal("expected injected crash")
	}
	before := *calls
	resumed, err := NewAgent(store, nil, base.model, Options{MaxSteps: 4, Telemetry: &signalFixture{}, TelemetryPolicy: "trusted-scope-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.Run(context.Background(), batch); err == nil {
		t.Fatal("changed scope policy accepted")
	}
	if *calls != before {
		t.Fatal("model called before rejecting incompatible checkpoint")
	}
}

// Exercise the actual completed checkpoint serializer, including every copy
// retained by the fallback artifact store and final result, at the query cap.
func TestSixBoundedTelemetryOutputsFitCompletedCheckpoint(t *testing.T) {
	store := &checkpointFixture{}
	a := &Agent{repo: store, options: Options{MaxSteps: 12, MaxReservedTokens: 50000, MaxContextChars: 24000, TelemetryPolicy: "bounded-v1"}}
	s := &agentState{batchID: uuid.NewString(), phase: "completed", artifacts: map[string]json.RawMessage{}, toolArtifacts: map[string]string{}, evidence: map[string]domain.EvidenceItem{}, result: &domain.DiagnosisResult{}}
	for queryIndex := 0; queryIndex < 6; queryIndex++ {
		items := []domain.EvidenceItem{}
		for item := 0; item < 7; item++ {
			evidence := domain.EvidenceItem{ID: fmt.Sprintf("log:bounded:%d:%d", queryIndex, item), Source: "loki:trusted-scope", Content: strings.Repeat("\"", 1000)}
			items = append(items, evidence)
			s.evidence[evidence.ID] = evidence
			s.result.Evidence = append(s.result.Evidence, evidence)
		}
		body, err := json.Marshal(toolOutput{Evidence: items})
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > 16*1024 {
			t.Fatalf("fixture violates returned query budget: %d", len(body))
		}
		key := fmt.Sprint(queryIndex)
		observation := domain.ToolObservation{Tool: "query_service_logs", Arguments: json.RawMessage(`{}`), Output: body}
		s.observations = append(s.observations, observation)
		s.result.ToolTrace = append(s.result.ToolTrace, observation)
		s.artifacts[key] = body
		s.toolArtifacts[key] = key
		s.messages = append(s.messages, schema.ToolMessage(string(body), key, schema.WithToolName("query_service_logs")))
	}
	if err := a.saveCheckpoint(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(store.data) > 1024*1024 {
		t.Fatalf("completed checkpoint exceeds 1MiB: %d", len(store.data))
	}
	restored := &agentState{batchID: s.batchID}
	if err := a.loadCheckpoint(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
	if restored.result == nil || len(restored.result.ToolTrace) != 6 {
		t.Fatal("completed recovery lost tool trace")
	}
	t.Logf("completed checkpoint bytes=%d", len(store.data))
}

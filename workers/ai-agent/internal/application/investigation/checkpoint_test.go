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

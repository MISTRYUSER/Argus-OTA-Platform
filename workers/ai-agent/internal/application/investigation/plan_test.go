package investigation

import (
	"context"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"testing"
)

func TestPlanRejectsInventedEvidence(t *testing.T) {
	s := &agentState{evidence: map[string]domain.EvidenceItem{"log:1": {ID: "log:1"}}}
	tool, err := planTool(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tool.InvokableRun(context.Background(), `{"hypotheses":[{"id":"H1","description":"connector failure","status":"supported","supports":["fake"],"next_check":"inspect"}]}`); err == nil {
		t.Fatal("invented support accepted")
	}
}
func TestPlanTracksSupportAndRefutation(t *testing.T) {
	s := &agentState{evidence: map[string]domain.EvidenceItem{"log:1": {ID: "log:1"}, "stats:E001": {ID: "stats:E001"}}}
	tool, err := planTool(s)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.InvokableRun(context.Background(), `{"hypotheses":[{"id":"H1","description":"connector failure","status":"supported","supports":["log:1"],"next_check":"inspect"},{"id":"H2","description":"firmware failure","status":"refuted","refutes":["stats:E001"],"next_check":"none"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.hypotheses) != 2 || s.hypotheses[1].Status != "refuted" {
		t.Fatal("plan not stored")
	}
}

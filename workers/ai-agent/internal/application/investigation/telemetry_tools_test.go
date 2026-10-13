package investigation

import (
	"context"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"testing"
)

type signalFixture struct{ ids []string }

func (f *signalFixture) QueryMetrics(_ context.Context, id string, _ domain.MetricQuery) ([]domain.EvidenceItem, error) {
	f.ids = append(f.ids, id)
	return []domain.EvidenceItem{{ID: "metric:fixture", Source: "prometheus:trusted-scope", Content: "CPU high"}}, nil
}
func (f *signalFixture) QueryLogs(_ context.Context, id string, _ domain.LogQuery) ([]domain.EvidenceItem, error) {
	f.ids = append(f.ids, id)
	return []domain.EvidenceItem{{ID: "log:remote", Source: "loki:trusted-scope", Content: "E001"}}, nil
}
func (f *signalFixture) QueryTraces(_ context.Context, id string, _ domain.TraceQuery) ([]domain.EvidenceItem, error) {
	f.ids = append(f.ids, id)
	return []domain.EvidenceItem{{ID: "trace:fixture", Source: "tempo:trusted-scope", Content: "upstream failing"}}, nil
}
func TestTelemetryToolsBindBatchAndBoundDurableQueries(t *testing.T) {
	f := &signalFixture{}
	s := &agentState{batchID: "assigned", observations: []domain.ToolObservation{}}
	tools, e := telemetryTools(s, f)
	if e != nil {
		t.Fatal(e)
	}
	for name, args := range map[string]string{"query_metrics": `{"metric":"cpu","namespace":"attacker"}`, "query_service_logs": `{}`, "query_traces": `{}`} {
		if _, e = tools[name].InvokableRun(context.Background(), args); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range f.ids {
		if id != "assigned" {
			t.Fatal("scope override")
		}
	}
	for i := 0; i < 6; i++ {
		s.observations = append(s.observations, domain.ToolObservation{Tool: "query_metrics"})
	}
	if _, e = tools["query_metrics"].InvokableRun(context.Background(), `{"metric":"cpu"}`); e == nil {
		t.Fatal("durable telemetry query limit reset")
	}
}

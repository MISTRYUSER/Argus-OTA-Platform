package hybrid

import (
	"context"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"math"
	"testing"
)

func history(scores ...float64) []domain.SimilarCase {
	out := []domain.SimilarCase{}
	for _, score := range scores {
		out = append(out, domain.SimilarCase{DiagnosisID: uuid.New(), BatchID: uuid.New(), Summary: "confirmed connector failure", Similarity: score, ScoreKind: "cosine", RootCauseID: "connector", Verified: true})
	}
	return out
}
func TestSemanticGateRequiresIndependentVerifiedAgreement(t *testing.T) {
	tests := []struct {
		name  string
		cases []domain.SimilarCase
		want  string
	}{
		{"agreement", history(.96, .95, .94), "semantic"},
		{"low", history(.71, .70, .69), "investigation"},
		{"gray", history(.8, .79, .78), "investigation"},
		{"boundary", history(.88, .87, .86), "investigation"},
		{"sparse", history(.99), "investigation"},
		{"nan", history(math.NaN(), .95, .94), "investigation"},
		{"out_of_range", history(1.01, .95, .94), "investigation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := semanticDecision(tt.cases, "current", DefaultThresholds())
			if d.Path != tt.want {
				t.Fatalf("got %+v", d)
			}
		})
	}
	for _, mutate := range []func([]domain.SimilarCase){
		func(c []domain.SimilarCase) { c[1].RootCauseID = "network" },
		func(c []domain.SimilarCase) { c[1].Verified = false },
		func(c []domain.SimilarCase) { c[1].ScoreKind = "text" },
		func(c []domain.SimilarCase) { c[1].DiagnosisID = c[0].DiagnosisID },
		func(c []domain.SimilarCase) { c[1].BatchID = c[0].BatchID },
		func(c []domain.SimilarCase) { c[1].RootCauseID = "" },
	} {
		c := history(.96, .95, .94)
		mutate(c)
		if d := semanticDecision(c, "current", DefaultThresholds()); d.Path != "investigation" {
			t.Fatalf("unsafe %+v", d)
		}
	}
	c := history(.96, .95, .94, .93)
	c[3].RootCauseID = "network"
	if d := semanticDecision(c, "current", DefaultThresholds()); d.Path != "investigation" {
		t.Fatalf("ambiguous competing root: %+v", d)
	}
	c = history(.96, .95, .94, .8)
	c[3].RootCauseID = "network"
	if d := semanticDecision(c, "current", DefaultThresholds()); d.Path != "semantic" {
		t.Fatalf("separated competitor: %+v", d)
	}
	c = history(.96, .95, .94)
	if d := semanticDecision(c, c[0].BatchID.String(), DefaultThresholds()); d.Path != "investigation" {
		t.Fatal("self retrieval accepted")
	}
}
func TestOOMRuleRequiresKernelEvidence(t *testing.T) {
	d := &domain.AggregatedData{LogsSummary: `{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}`, RawLogs: `{"error_type":"OOM_KILL","module_name":"planner","exception_type":"OOMKilled","exit_code":137,"oom_killed":true}`, ErrorCodeStats: map[string]int{"OOM_KILL": 1}}
	r, _ := NewRunner(repository{data: d}, nil, budgetRunner{}, Options{})
	out, err := r.Run(context.Background(), "b")
	if err != nil || out.Model != "deterministic-rules-v1" || out.ModelSteps != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	d.RawLogs = `{"error_type":"OOM_KILL","module_name":"planner","exception_type":"OOMKilled","exit_code":137}`
	out, err = r.Run(context.Background(), "b")
	if err != nil || out.ResolutionStatus != "unresolved" {
		t.Fatalf("exit 137 alone accepted: %+v %v", out, err)
	}
}

type gateRetriever struct {
	domain.VectorRetriever
	cases []domain.SimilarCase
	err   error
}

func (r gateRetriever) Search(context.Context, domain.SearchParams) ([]domain.SimilarCase, error) {
	return r.cases, r.err
}
func TestRoutingSignatureAndRetrievalFailure(t *testing.T) {
	d := &domain.AggregatedData{LogsSummary: `{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}`, RawLogs: `{"error_type":"UNKNOWN","module":"planner","exception_type":"timeout"}`, ErrorCodeStats: map[string]int{"UNKNOWN": 1}}
	cases := history(.96, .95, .94)
	for i := range cases {
		cases[i].ModuleName = "planner"
		cases[i].ExceptionType = "timeout"
	}
	r, _ := NewRunner(repository{data: d}, nil, budgetRunner{}, Options{Retriever: gateRetriever{cases: cases}})
	decision, _ := r.route(context.Background(), d, "current")
	if decision.Path != "semantic" {
		t.Fatalf("valid scoped history: %+v", decision)
	}
	cases[0].ModuleName = "camera"
	decision, _ = r.route(context.Background(), d, "current")
	if decision.Path != "investigation" {
		t.Fatal("cross-module diagnosis accepted")
	}
	r.options.Retriever = gateRetriever{err: context.DeadlineExceeded}
	decision, _ = r.route(context.Background(), d, "current")
	if decision.Path != "investigation" || decision.Reason != "historical retrieval failed" {
		t.Fatalf("outage: %+v", decision)
	}
	d.RawLogs = `{"error_type":"OTHER","module":"planner"}`
	if _, _, ok := signature(d); ok {
		t.Fatal("conflicting signature accepted")
	}
}
func TestRejectInvalidRoutingConfiguration(t *testing.T) {
	for _, thresholds := range []Thresholds{{High: .7, Low: .8, Margin: .05}, {High: math.NaN(), Low: .7, Margin: .05}, {High: .9, Low: .7, Margin: 0}} {
		if _, err := NewRunner(repository{}, nil, budgetRunner{}, Options{Thresholds: thresholds}); err == nil {
			t.Fatalf("invalid thresholds accepted: %+v", thresholds)
		}
	}
	if _, err := NewRunner(repository{}, nil, budgetRunner{}, Options{Rules: []Rule{{ID: "unsafe", ErrorCode: "UNKNOWN", Predicate: "model_confidence"}}}); err == nil {
		t.Fatal("unaudited predicate accepted")
	}
}

package hybrid

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"testing"
)

func TestRouteRequiresExplicitCompleteNonConflictingEvidence(t *testing.T) {
	valid := &domain.AggregatedData{LogsSummary: `{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}`, RawLogs: `{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"a","actual_checksum":"b"}`, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 1}}
	if ok, _ := Eligible(valid); !ok {
		t.Fatal("complete evidence not eligible")
	}
	for _, d := range []*domain.AggregatedData{nil, {RawLogs: "timeout confidence=1", ErrorCodeStats: map[string]int{"E001": 1}}, {RawLogs: valid.RawLogs, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 2}}, {RawLogs: `{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"a","actual_checksum":"a"}`, ErrorCodeStats: valid.ErrorCodeStats}, {RawLogs: valid.RawLogs, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 1, "OTHER": 1}}} {
		if d != nil {
			d.LogsSummary = valid.LogsSummary
		}
		if ok, _ := Eligible(d); ok {
			t.Fatalf("unsafe route %+v", d)
		}
	}
	missingCoverage := *valid
	missingCoverage.LogsSummary = ""
	if ok, _ := Eligible(&missingCoverage); ok {
		t.Fatal("missing coverage accepted")
	}
}
func TestRejectInventedAndMissingCurrentEvidence(t *testing.T) {
	ev := []domain.EvidenceItem{{ID: "log:1", Source: "aggregate:b:line:1", Content: "checksum"}, {ID: "stats:1", Source: "aggregate:b:error_statistics", Content: "1"}}
	for _, ids := range [][]string{{"invented"}, {"stats:1"}, {"log:1"}} {
		if err := ValidateReferences(ids, ev); !errors.Is(err, domain.ErrInvalidDiagnosis) {
			t.Fatalf("accepted %v: %v", ids, err)
		}
	}
	if err := ValidateReferences([]string{"log:1", "stats:1"}, ev); err != nil {
		t.Fatal(err)
	}
}

type budgetRunner struct{}

func (budgetRunner) Run(context.Context, string) (*domain.DiagnosisResult, error) {
	return nil, domain.ErrInvestigationBudget
}

type repository struct {
	domain.DiagnosisRepository
	data *domain.AggregatedData
}

func (r repository) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return r.data, nil
}
func TestFallbackBudgetProducesReviewReport(t *testing.T) {
	r, err := NewRunner(repository{data: &domain.AggregatedData{RawLogs: "ambiguous", ErrorCodeStats: map[string]int{"OTHER": 1}}}, nil, budgetRunner{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), "b")
	if err != nil || out == nil || out.Confidence != 0 || len(out.Limitations) == 0 || out.Hypotheses[0].Status != "open" || !json.Valid([]byte(out.RawLLMOutput)) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

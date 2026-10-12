package nodes

import (
	"encoding/json"
	"fmt"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"math"
	"strings"
)

func ParseDiagnosisResult(content string) (*domain.DiagnosisResult, error) {
	raw := cleanJSON(content)
	var v struct {
		HypothesisID string   `json:"hypothesis_id"`
		RootCause    string   `json:"root_cause"`
		Suggestions  []string `json:"suggestions"`
		Severity     string   `json:"severity"`
		Confidence   *float64 `json:"confidence"`
		EvidenceIDs  []string `json:"evidence_ids"`
		Limitations  []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %v", domain.ErrInvalidDiagnosis, err)
	}
	if v.Confidence == nil || math.IsNaN(*v.Confidence) || math.IsInf(*v.Confidence, 0) {
		return nil, fmt.Errorf("%w: confidence is missing or invalid", domain.ErrInvalidDiagnosis)
	}
	r := &domain.DiagnosisResult{HypothesisID: v.HypothesisID, RootCause: strings.TrimSpace(v.RootCause), Suggestions: v.Suggestions, Severity: v.Severity, Confidence: *v.Confidence, RawLLMOutput: raw, EvidenceIDs: v.EvidenceIDs, Limitations: v.Limitations}
	if !isValidDiagnosisResult(r) {
		return nil, fmt.Errorf("%w: invalid fields", domain.ErrInvalidDiagnosis)
	}
	return r, nil
}

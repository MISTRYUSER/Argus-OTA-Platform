package hybrid

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"math"
	"sort"
	"strings"
)

const policyVersion = "confidence-routing-v1"

type Thresholds struct {
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Margin float64 `json:"margin"`
}

func DefaultThresholds() Thresholds { return Thresholds{High: .88, Low: .72, Margin: .05} }
func (t Thresholds) Validate() error {
	for _, v := range []float64{t.High, t.Low, t.Margin} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("non-finite routing threshold")
		}
	}
	if t.Low < 0 || t.High <= t.Low || t.High > 1 || t.Margin <= 0 || t.Margin > 1 {
		return fmt.Errorf("invalid routing thresholds")
	}
	return nil
}

// Rule is an operator-controlled exact mapping. Predicate names refer to audited
// code, never expressions supplied by a log or a model.
type Rule struct {
	ID        string `json:"id"`
	ErrorCode string `json:"error_code"`
	Module    string `json:"module,omitempty"`
	Exception string `json:"exception,omitempty"`
	Predicate string `json:"predicate"`
}

func DefaultRules() []Rule {
	return []Rule{
		{ID: "ota-checksum-v1", ErrorCode: "OTA_CHECKSUM_MISMATCH", Predicate: "checksum"},
		{ID: "ota-timeout-v1", ErrorCode: "OTA_DOWNLOAD_TIMEOUT", Predicate: "timeout"},
		{ID: "kernel-oom-v1", ErrorCode: "OOM_KILL", Exception: "OOMKilled", Predicate: "oom"},
	}
}
func ValidateRules(rules []Rule) error {
	ids := map[string]bool{}
	for _, r := range rules {
		if r.ID == "" || ids[r.ID] || r.ErrorCode == "" {
			return fmt.Errorf("invalid or duplicate deterministic rule")
		}
		ids[r.ID] = true
		if r.Predicate != "checksum" && r.Predicate != "timeout" && r.Predicate != "oom" {
			return fmt.Errorf("unknown audited predicate %q", r.Predicate)
		}
	}
	return nil
}
func covered(d *domain.AggregatedData) bool {
	if d == nil || len(d.RawLogs) > 16000 || strings.TrimSpace(d.RawLogs) == "" || len(d.ErrorCodeStats) != 1 {
		return false
	}
	var c struct {
		Schema   string `json:"hybrid_evidence_schema"`
		Grain    string `json:"stats_grain"`
		Complete bool   `json:"complete"`
	}
	if json.Unmarshal([]byte(d.LogsSummary), &c) != nil || c.Schema != "v1" || c.Grain != "log_occurrence" || !c.Complete {
		return false
	}
	lines := strings.Split(strings.TrimSpace(d.RawLogs), "\n")
	if len(lines) > 100 {
		return false
	}
	for code, n := range d.ErrorCodeStats {
		if code == "" || n != len(lines) {
			return false
		}
	}
	return true
}
func matchRule(d *domain.AggregatedData, rules []Rule) (Rule, bool) {
	if !covered(d) {
		return Rule{}, false
	}
	for _, rule := range rules {
		if d.ErrorCodeStats[rule.ErrorCode] == 0 {
			continue
		}
		matched := true
		for _, line := range strings.Split(strings.TrimSpace(d.RawLogs), "\n") {
			var v struct {
				Code        string   `json:"error_type"`
				Module      string   `json:"module_name"`
				ModuleAlias string   `json:"module"`
				Exception   string   `json:"exception_type"`
				Expected    string   `json:"expected_checksum"`
				Actual      string   `json:"actual_checksum"`
				Elapsed     *float64 `json:"elapsed_ms"`
				Timeout     *float64 `json:"timeout_ms"`
				Exit        *int     `json:"exit_code"`
				OOM         bool     `json:"oom_killed"`
			}
			if json.Unmarshal([]byte(line), &v) != nil {
				matched = false
				break
			}
			if v.Module == "" {
				v.Module = v.ModuleAlias
			}
			if v.Code != rule.ErrorCode || (rule.Module != "" && v.Module != rule.Module) || (rule.Exception != "" && v.Exception != rule.Exception) {
				matched = false
				break
			}
			switch rule.Predicate {
			case "checksum":
				matched = v.Expected != "" && v.Actual != "" && v.Expected != v.Actual
			case "timeout":
				matched = v.Elapsed != nil && v.Timeout != nil && *v.Timeout > 0 && *v.Elapsed > *v.Timeout
			case "oom":
				matched = v.Module != "" && v.Exception == "OOMKilled" && v.Exit != nil && *v.Exit == 137 && v.OOM
			}
			if !matched {
				break
			}
		}
		if matched {
			return rule, true
		}
	}
	return Rule{}, false
}
func semanticDecision(cases []domain.SimilarCase, id string, t Thresholds) domain.RouteDecision {
	d := domain.RouteDecision{Path: "investigation", Reason: "insufficient verified historical cases", PolicyVersion: policyVersion}
	if len(cases) < 3 || len(cases) > 5 {
		return d
	}
	cases = append([]domain.SimilarCase(nil), cases...)
	seen, batches := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, c := range cases {
		if c.DiagnosisID == uuid.Nil || c.BatchID == uuid.Nil || c.BatchID.String() == id || seen[c.DiagnosisID] || batches[c.BatchID] || !c.Verified || c.RootCauseID == "" || c.ScoreKind != "cosine" || strings.TrimSpace(c.Summary) == "" || len(c.Summary) > 4000 || math.IsNaN(c.Similarity) || math.IsInf(c.Similarity, 0) || c.Similarity < -1 || c.Similarity > 1 {
			d.Reason = "untrusted, duplicate, self, or invalid retrieval evidence"
			return d
		}
		seen[c.DiagnosisID] = true
		batches[c.BatchID] = true
	}
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Similarity > cases[j].Similarity })
	d.TopSimilarity = cases[0].Similarity
	d.Score = math.Max(0, d.TopSimilarity)
	if d.TopSimilarity < t.Low {
		d.Reason = "similarity below low threshold"
		return d
	}
	if d.TopSimilarity <= t.High {
		d.Reason = "similarity in gray zone"
		return d
	}
	if cases[0].RootCauseID != cases[1].RootCauseID || cases[0].RootCauseID != cases[2].RootCauseID || cases[2].Similarity < t.Low {
		d.Reason = "top three causes disagree or supporting similarity too low"
		return d
	}
	// Margin is between distinct causes: same-cause neighbors should not penalize agreement.
	d.Margin = 1
	for _, c := range cases[3:] {
		if c.RootCauseID != cases[0].RootCauseID {
			d.Margin = d.TopSimilarity - c.Similarity
			break
		}
	}
	if d.Margin < t.Margin {
		d.Reason = "competing root cause too close"
		return d
	}
	d.Score = .8*d.TopSimilarity + .2*math.Min(1, d.Margin/.1)
	d.Path = "semantic"
	d.Reason = "verified top-three agreement and separated competing causes"
	for _, c := range cases[:3] {
		d.CaseIDs = append(d.CaseIDs, c.DiagnosisID.String())
	}
	return d
}
func (r *Runner) route(ctx context.Context, d *domain.AggregatedData, id string) (domain.RouteDecision, []domain.SimilarCase) {
	decision := domain.RouteDecision{Path: "investigation", Reason: "missing, mixed, or incomplete evidence", PolicyVersion: policyVersion}
	if !covered(d) {
		return decision, nil
	}
	if rule, ok := matchRule(d, r.options.Rules); ok {
		decision.Path = "deterministic"
		decision.RuleID = rule.ID
		decision.Reason = rule.Predicate
		decision.Score = 1
		return decision, nil
	}
	if r.options.Retriever == nil {
		decision.Reason = "historical retrieval unavailable"
		return decision, nil
	}
	codes := []string{}
	for code := range d.ErrorCodeStats {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	child, cancel := context.WithTimeout(ctx, r.options.RetrievalTimeout)
	defer cancel()
	cases, err := r.options.Retriever.Search(child, domain.SearchParams{EmbeddingText: d.RawLogs, ErrorCodes: codes, TopK: 5, ExcludeBatchID: id})
	if err != nil || child.Err() != nil {
		decision.Reason = "historical retrieval failed"
		return decision, nil
	}
	module, exception, valid := signature(d)
	if !valid {
		decision.Reason = "untyped or conflicting current signature"
		return decision, nil
	}
	for _, c := range cases {
		if c.ModuleName != module || c.ExceptionType != exception {
			decision.Reason = "historical module or exception differs"
			return decision, nil
		}
	}
	decision = semanticDecision(cases, id, r.options.Thresholds)
	return decision, cases
}
func deterministicReport(d *domain.AggregatedData, ev []domain.EvidenceItem, decision domain.RouteDecision) *domain.DiagnosisResult {
	descriptions := map[string]string{"checksum": "Observed artifact checksum mismatch", "timeout": "Observed download duration exceeded configured timeout", "oom": "Kernel-confirmed OOM termination (exit 137)"}
	out := &domain.DiagnosisResult{Routing: &decision, ResolutionStatus: "provisional", Model: "deterministic-rules-v1", RootCause: descriptions[decision.Reason] + "; upstream cause requires independent verification", Severity: "high", Confidence: 0, Evidence: ev, HypothesisID: "H1", Suggestions: []string{"Inspect the current evidence and verify the upstream cause independently"}, Limitations: []string{"Exact rule match confirms the observation only; routing score 1 is not root-cause probability"}}
	for _, e := range ev {
		out.EvidenceIDs = append(out.EvidenceIDs, e.ID)
	}
	out.Hypotheses = []domain.Hypothesis{{ID: "H1", Description: out.RootCause, Status: "open", Supports: out.EvidenceIDs, NextCheck: "Verify upstream cause independently"}}
	b, _ := json.Marshal(map[string]interface{}{"hypothesis_id": "H1", "root_cause": out.RootCause, "suggestions": out.Suggestions, "severity": out.Severity, "confidence": 0, "evidence_ids": out.EvidenceIDs, "limitations": out.Limitations})
	out.RawLLMOutput = string(b)
	return out
}

func signature(d *domain.AggregatedData) (string, string, bool) {
	module, exception := "", ""
	for i, line := range strings.Split(strings.TrimSpace(d.RawLogs), "\n") {
		var v struct {
			Code       string `json:"error_type"`
			Module     string `json:"module"`
			ModuleName string `json:"module_name"`
			Exception  string `json:"exception_type"`
		}
		if json.Unmarshal([]byte(line), &v) != nil || d.ErrorCodeStats[v.Code] == 0 {
			return "", "", false
		}
		if v.ModuleName != "" {
			if v.Module != "" && v.Module != v.ModuleName {
				return "", "", false
			}
			v.Module = v.ModuleName
		}
		if v.Module == "" {
			return "", "", false
		}
		if i == 0 {
			module, exception = v.Module, v.Exception
		} else if module != v.Module || exception != v.Exception {
			return "", "", false
		}
	}
	return module, exception, true
}

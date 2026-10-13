package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/hybrid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/investigation"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"time"
)

type fixtureRow struct {
	Case      string                  `json:"case"`
	Mode      string                  `json:"mode"`
	Status    string                  `json:"status"`
	Error     string                  `json:"error,omitempty"`
	HTTPCalls int                     `json:"actual_http_calls"`
	Tokens    int                     `json:"provider_reported_synthetic_tokens"`
	LatencyMS float64                 `json:"latency_ms"`
	CostUSD   *float64                `json:"cost_usd"`
	Result    *domain.DiagnosisResult `json:"result,omitempty"`
}
type fixtureSummary struct {
	Attempts   int     `json:"attempts"`
	Successes  int     `json:"successes"`
	Failures   int     `json:"failures"`
	Unresolved int     `json:"unresolved"`
	HTTPCalls  int     `json:"actual_http_calls"`
	Tokens     int     `json:"provider_reported_synthetic_tokens"`
	P95MS      float64 `json:"latency_p95_ms"`
}
type fixtureReport struct {
	Synthetic bool                      `json:"synthetic"`
	Scope     string                    `json:"scope"`
	Rows      []fixtureRow              `json:"attempts"`
	Summary   map[string]fixtureSummary `json:"summary"`
}

const fixtureCoverage = `{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}`

// runHybridFixture measures production HTTP round trips against a local scripted
// provider. The script never represents independent diagnosis quality or billing.
func runHybridFixture(path string) error {
	checksum := `{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"abc","actual_checksum":"def"}`
	cases := []struct {
		name, log, coverage, code string
		budget                    int
		invalid                   bool
	}{
		{"checksum", checksum, fixtureCoverage, "OTA_CHECKSUM_MISMATCH", 1000000, false},
		{"timeout", `{"error_type":"OTA_DOWNLOAD_TIMEOUT","elapsed_ms":200,"timeout_ms":100}`, fixtureCoverage, "OTA_DOWNLOAD_TIMEOUT", 1000000, false},
		{"unknown", "E_OTHER: ambiguous symptom", fixtureCoverage, "E_OTHER", 1000000, false},
		{"conflict", `{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"abc","actual_checksum":"abc"}`, fixtureCoverage, "OTA_CHECKSUM_MISMATCH", 1000000, false},
		{"invented-reference", checksum, fixtureCoverage, "OTA_CHECKSUM_MISMATCH", 1000000, true},
		{"budget-exhausted", checksum, fixtureCoverage, "OTA_CHECKSUM_MISMATCH", 1, false},
	}
	report := fixtureReport{Synthetic: true, Scope: "Functional fixed-workflow routing and measured local HTTP call savings only; scripted answers and tokens do not establish real model quality, latency, or fees. Cost unknown. Cases are independent of ITBench labels.", Summary: map[string]fixtureSummary{}}
	for _, mode := range []string{"agent", "hybrid"} {
		for _, c := range cases {
			calls := 0
			tokens := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct {
					Messages []struct{ Role, Content string }
				}
				json.NewDecoder(req.Body).Decode(&body)
				fixed := len(body.Messages) > 0 && strings.Contains(body.Messages[0].Content, "Summarize the supplied observations")
				message := map[string]interface{}{"role": "assistant"}
				if !fixed && calls == 1 {
					tc := []interface{}{}
					for _, item := range []struct{ name, args string }{{"read_logs", "{}"}, {"error_statistics", "{}"}, {"update_investigation_plan", `{"hypotheses":[{"id":"H1","description":"Observed failure, upstream cause unknown","status":"supported","supports":["log:1"],"next_check":"Inspect upstream cause"}]}`}} {
						tc = append(tc, map[string]interface{}{"id": item.name, "type": "function", "function": map[string]string{"name": item.name, "arguments": item.args}})
					}
					message["tool_calls"] = tc
					message["content"] = ""
				} else {
					stat := "stats:" + c.code
					if fixed {
						stat = "stats:1"
					}
					ids := []string{"log:1", stat}
					if c.invalid {
						ids = []string{"invented"}
					}
					out := map[string]interface{}{"hypothesis_id": "H1", "root_cause": "Observed failure; upstream cause unknown", "suggestions": []string{"Inspect upstream cause"}, "severity": "high", "confidence": 0.5, "evidence_ids": ids, "limitations": []string{"Synthetic scripted response"}}
					b, _ := json.Marshal(out)
					message["content"] = string(b)
				}
				tokens += 30
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"id": "synthetic", "choices": []interface{}{map[string]interface{}{"index": 0, "message": message, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30}})
			}))
			start := time.Now()
			store := &repo{lines: []string{c.log}, errorCodeStats: map[string]int{c.code: 1}, logsSummary: c.coverage}
			chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "synthetic-local-only", Model: "fixture", BaseURL: server.URL + "/"})
			var result *domain.DiagnosisResult
			if err == nil {
				a, e := investigation.NewAgent(store, nil, chat, investigation.Options{MaxSteps: 4, MaxReservedTokens: c.budget, ModelName: "fixture"})
				err = e
				if err == nil {
					var runner domain.DiagnosisRunner = a
					if mode == "hybrid" {
						runner, err = hybrid.NewRunner(store, chat, a, hybrid.Options{MaxReservedTokens: c.budget, ModelName: "fixture"})
					}
					if err == nil {
						result, err = runner.Run(context.Background(), uuid.NewString())
					}
				}
			}
			row := fixtureRow{Case: c.name, Mode: mode, Status: "success", HTTPCalls: calls, Tokens: tokens, LatencyMS: float64(time.Since(start).Microseconds()) / 1000, Result: result}
			if err != nil {
				row.Status = "failure"
				row.Error = err.Error()
			}
			if result != nil && strings.HasPrefix(result.RootCause, "Unresolved:") {
				row.Status = "unresolved"
			}
			report.Rows = append(report.Rows, row)
			server.Close()
		}
	}
	for _, mode := range []string{"agent", "hybrid"} {
		report.Summary[mode] = summarizeFixture(report.Rows, mode)
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err == nil {
		return fmt.Errorf("fixture output exists; preserve prior run")
	}
	return os.WriteFile(path, b, 0644)
}
func summarizeFixture(rows []fixtureRow, mode string) fixtureSummary {
	s := fixtureSummary{}
	lat := []float64{}
	for _, r := range rows {
		if r.Mode != mode {
			continue
		}
		s.Attempts++
		s.HTTPCalls += r.HTTPCalls
		s.Tokens += r.Tokens
		lat = append(lat, r.LatencyMS)
		switch r.Status {
		case "success":
			s.Successes++
		case "unresolved":
			s.Unresolved++
		default:
			s.Failures++
		}
	}
	sort.Float64s(lat)
	if len(lat) > 0 {
		s.P95MS = lat[(95*len(lat)+99)/100-1]
	}
	return s
}

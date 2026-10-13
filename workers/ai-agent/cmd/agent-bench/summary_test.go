package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSummaryIncludesFailuresAndUnresolved(t *testing.T) {
	dir := t.TempDir()
	for i, status := range []string{"success", "failure", "unresolved"} {
		p := filepath.Join(dir, string(rune('a'+i)))
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]interface{}{"status": status, "latency_ms": i + 1, "actual_http_calls": i + 1, "provider_stats": map[string]interface{}{"Tokens": 5}, "valid_report": status != "failure"})
		if err := os.WriteFile(filepath.Join(p, "argus_run.json"), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeEvaluationSummary(dir, "hybrid", "heldout"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "summary.json"))
	var s evaluationSummary
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Attempts != 3 || s.Failures != 1 || s.Unresolved != 1 || s.HTTPCalls != 6 || s.Tokens != 15 || s.P95MS != 3 || s.CostUSD != nil {
		t.Fatalf("summary %+v", s)
	}
}

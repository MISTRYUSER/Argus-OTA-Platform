package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSyntheticHybridBenchmarkCountsEveryAttempt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.json")
	if err := runHybridFixture(p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var report fixtureReport
	if err = json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Synthetic || len(report.Rows) != 12 {
		t.Fatalf("missing attempts: %+v", report)
	}
	if report.Summary["agent"].Attempts != 6 || report.Summary["hybrid"].Attempts != 6 {
		t.Fatal("wrong denominator")
	}
	if report.Summary["agent"].HTTPCalls <= report.Summary["hybrid"].HTTPCalls {
		t.Fatal("no functional call savings")
	}
	for _, row := range report.Rows {
		if row.CostUSD != nil {
			t.Fatal("fixture fees fabricated")
		}
	}
}

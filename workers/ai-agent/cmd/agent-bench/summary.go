package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type evaluationSummary struct {
	Mode          string   `json:"mode"`
	Partition     string   `json:"partition"`
	Attempts      int      `json:"attempts"`
	Successes     int      `json:"successes"`
	Failures      int      `json:"failures"`
	Unresolved    int      `json:"unresolved"`
	HTTPCalls     int      `json:"actual_http_calls"`
	Tokens        int64    `json:"provider_reported_tokens"`
	P95MS         float64  `json:"latency_p95_ms"`
	CostUSD       *float64 `json:"cost_usd"`
	QualityStatus string   `json:"quality_status"`
	CostStatus    string   `json:"cost_status"`
}

func writeEvaluationSummary(dir, mode, partition string) error {
	s := evaluationSummary{Mode: mode, Partition: partition, QualityStatus: "Requires independent label-based scorer; valid JSON and confidence are not quality scores", CostStatus: "unknown: pricing not configured"}
	lat := []float64{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || entry.Name() != "argus_run.json" {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var r struct {
			Status  string                 `json:"status"`
			Valid   bool                   `json:"valid_report"`
			Latency float64                `json:"latency_ms"`
			Calls   int                    `json:"actual_http_calls"`
			Stats   struct{ Tokens int64 } `json:"provider_stats"`
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return fmt.Errorf("invalid attempt %s: %w", path, e)
		}
		s.Attempts++
		s.HTTPCalls += r.Calls
		s.Tokens += r.Stats.Tokens
		lat = append(lat, r.Latency)
		if r.Status == "unresolved" {
			s.Unresolved++
		} else if r.Valid {
			s.Successes++
		} else {
			s.Failures++
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Float64s(lat)
	if len(lat) > 0 {
		s.P95MS = lat[(95*len(lat)+99)/100-1]
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(dir, "summary.json"), b, 0644)
}

package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPythonProcessesActualCSVAndTracksSources(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "metrics.csv")
	content := "timestamp,version,vehicle_id,vehicle_class,module,replay_id,workload,error_code,cpu,memory_mb\n"
	for _, v := range []string{"v1", "v2"} {
		for i := 0; i < 10; i++ {
			cpu := "20"
			code := ""
			if v == "v2" {
				cpu = "40"
				code = "E42"
			}
			content += fmtRow(i, v, code, cpu)
		}
	}
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	py := Python{Script: filepath.Join("..", "..", "..", "workers", "python-aggregator", "regression.py"), Baseline: "v1", Candidate: "v2"}
	raw, e := py.Parse(context.Background(), p, "minio:batch/metrics.csv")
	if e != nil {
		t.Fatal(e)
	}
	var rows []json.RawMessage
	if e = json.Unmarshal(raw, &rows); e != nil || len(rows) != 20 {
		t.Fatalf("parse %d %v", len(rows), e)
	}
	report, e := py.Analyze(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	var d struct {
		Status   string `json:"status"`
		Matched  int    `json:"matched_samples"`
		Evidence []struct {
			Source string `json:"source"`
		} `json:"evidence"`
	}
	if e = json.Unmarshal(report, &d); e != nil {
		t.Fatal(e)
	}
	if d.Status != "comparable" || d.Matched != 20 || len(d.Evidence) == 0 || d.Evidence[0].Source != "minio:batch/metrics.csv" {
		t.Fatalf("bad report %s", report)
	}
}
func fmtRow(i int, v, code, cpu string) string {
	return fmt.Sprintf("%d,%s,V1,truck,planner,R1,AD,%s,%s,100\n", 1000+i, v, code, cpu)
}
func TestPythonRejectsInvalidMetrics(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.csv")
	os.WriteFile(p, []byte("not-valid\n"), 0600)
	py := Python{Script: filepath.Join("..", "..", "..", "workers", "python-aggregator", "regression.py")}
	if _, e := py.Parse(context.Background(), p, "source"); e == nil {
		t.Fatal("invalid input silently accepted")
	}
}
func TestPythonCancellation(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Python{}).Analyze(c, []byte("[]")); e == nil {
		t.Fatal("canceled processing ran")
	}
}

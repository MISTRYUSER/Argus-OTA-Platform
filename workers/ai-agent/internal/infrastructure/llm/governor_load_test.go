package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGovernorMeasuredLoad(t *testing.T) {
	if os.Getenv("ARGUS_LOAD_EVAL") != "1" {
		t.Skip("opt-in local controlled-provider load evaluation")
	}
	out := os.Getenv("ARGUS_LOAD_REPORT_DIR")
	if out == "" {
		t.Fatal("report directory required")
	}
	os.MkdirAll(out, 0755)
	summaries := []map[string]interface{}{}
	for _, profile := range []struct {
		name                  string
		concurrency, attempts int
	}{{"no_retry_c2", 2, 1}, {"retry_c2", 2, 3}, {"retry_c8", 8, 3}} {
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			time.Sleep(5 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			if n%7 == 0 {
				w.WriteHeader(429)
				w.Write([]byte(`{"error":{"message":"controlled rate limit","type":"rate_limit","code":"rate_limit"}}`))
				return
			}
			w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"}}],"usage":{"total_tokens":100}}`))
		}))
		policy := GovernorOptions{Concurrency: profile.concurrency, QueueCapacity: 128, Attempts: profile.attempts, Backoff: 2 * time.Millisecond}
		chat, e := NewGLM4ChatModel(&GLM4Config{APIKey: "fixture", Model: "controlled", BaseURL: server.URL, Governor: &policy})
		if e != nil {
			t.Fatal(e)
		}
		g := chat.(*GovernedModel)
		type row struct {
			ID        int     `json:"id"`
			LatencyMS float64 `json:"latency_ms"`
			Success   bool    `json:"success"`
			Error     string  `json:"error,omitempty"`
		}
		rows := make([]row, 1000)
		jobs := make(chan int)
		var wg sync.WaitGroup
		start := time.Now()
		for w := 0; w < 32; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					begin := time.Now()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_, e := g.Generate(ctx, nil)
					cancel()
					rows[i] = row{ID: i, LatencyMS: float64(time.Since(begin).Microseconds()) / 1000, Success: e == nil}
					if e != nil {
						rows[i].Error = e.Error()
					}
				}
			}()
		}
		for i := range rows {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		elapsed := time.Since(start)
		server.Close()
		lat := make([]float64, len(rows))
		success := 0
		for i, r := range rows {
			lat[i] = r.LatencyMS
			if r.Success {
				success++
			}
		}
		sort.Float64s(lat)
		stats := g.Snapshot()
		if stats.Active != 0 || stats.Queued != 0 || stats.PeakActive > int64(profile.concurrency) {
			t.Fatalf("admission bound violated %+v", stats)
		}
		if profile.attempts == 3 && (stats.Attempts <= 1000 || stats.Attempts > 3000 || stats.Retries == 0 || stats.Completed != int64(success)) {
			t.Fatalf("retry bounds/usage invalid: %+v", stats)
		}
		data, _ := json.MarshalIndent(rows, "", "  ")
		if e = os.WriteFile(filepath.Join(out, profile.name+"-rows.json"), data, 0644); e != nil {
			t.Fatal(e)
		}
		summaries = append(summaries, map[string]interface{}{"profile": profile.name, "requests": len(rows), "success": success, "elapsed_ms": elapsed.Milliseconds(), "logical_requests_per_second": float64(len(rows)) / elapsed.Seconds(), "p50_ms": lat[499], "p95_ms": lat[949], "p99_ms": lat[989], "stats": stats, "provider": "local HTTP fixture, 5ms per attempt, every seventh attempt returns 429; token usage synthetic, not billable", "clients": 32, "policy": policy})
	}
	data, _ := json.MarshalIndent(summaries, "", "  ")
	if e := os.WriteFile(filepath.Join(out, "load-summary.json"), data, 0644); e != nil {
		t.Fatal(e)
	}
}

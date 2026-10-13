package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	pg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

// This is deliberately a real subprocess test, not two objects in one process.
// Only the HTTP provider is a local fixture; PostgreSQL leases, checkpoints,
// fencing, result/outbox transactions, SIGKILL and HTTP transport are real.
func TestSharedQuotaProcessHelper(t *testing.T) {
	if os.Getenv("ARGUS_QUOTA_HELPER") != "1" {
		return
	}
	db, e := sql.Open("postgres", os.Getenv("ARGUS_QUOTA_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, stop := context.WithTimeout(context.Background(), 50*time.Second)
	defer stop()
	quota, e := llm.NewPostgresQuota(ctx, db, "multiprocess-fixture", 2, 2*time.Second, 200*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	policy := llm.GovernorOptions{Concurrency: 2, Attempts: 3, Backoff: time.Millisecond, RequestTimeout: 2 * time.Second, Shared: quota}
	chat, e := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: os.Getenv("ARGUS_QUOTA_HTTP"), Governor: &policy})
	if e != nil {
		t.Fatal(e)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	remaining := strings.Split(os.Getenv("ARGUS_QUOTA_BATCHES"), ",")
	for len(remaining) > 0 {
		next := []string{}
		for _, id := range remaining {
			_, e := r.(domain.LeasedDiagnosisRepository).ExecuteOnce(ctx, id, func(runCtx context.Context) (*domain.Diagnosis, error) {
				check := r.(domain.InvestigationCheckpointStore)
				saved, e := check.LoadInvestigationCheckpoint(runCtx, id)
				if e != nil {
					return nil, e
				}
				if len(saved) > 0 && string(saved) != `{"phase":"evidence-collected"}` {
					return nil, fmt.Errorf("checkpoint corrupted")
				}
				if len(saved) == 0 {
					if e = check.SaveInvestigationCheckpoint(runCtx, id, []byte(`{"phase":"evidence-collected"}`)); e != nil {
						return nil, e
					}
				}
				_, e = chat.Generate(runCtx, []*schema.Message{schema.UserMessage(id)})
				if e != nil {
					return nil, e
				}
				now := time.Now()
				return &domain.Diagnosis{ID: uuid.New(), BatchID: uuid.MustParse(id), Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "fixture completed", Model: "fixture", CreatedAt: now, UpdatedAt: now}, nil
			})
			if e != nil {
				var deferred *core.DeferredTaskError
				if !errors.As(e, &deferred) && !errors.Is(e, llm.ErrModelQueueFull) {
					t.Fatal(e)
				}
				next = append(next, id)
			}
		}
		remaining = next
		if len(remaining) > 0 {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

func TestSharedQuotaMultiprocessCrashRecovery(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	var search string
	if e := db.QueryRow(`SHOW search_path`).Scan(&search); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(os.Getenv("ARGUS_TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+strings.Split(search, ",")[0]+",public")
	u.RawQuery = q.Encode()
	ids := []string{}
	for i := 0; i < 12; i++ {
		b, _ := core.NewBatch("fixture", "VIN", 1)
		b.ClearEvents()
		if e = corepg.NewPostgresBatchRepository(db).Save(ctx, b); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, b.ID.String())
	}
	started := make(chan string, 4)
	var calls, active, peak, limited atomic.Int64
	var mu sync.Mutex
	crashSeen := map[string]bool{}
	crashed := map[string]bool{ids[0]: true, ids[1]: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		id := ""
		if len(body.Messages) > 0 {
			id = body.Messages[0].Content
		}
		n := calls.Add(1)
		a := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); a > p; p = peak.Load() {
			if peak.CompareAndSwap(p, a) {
				break
			}
		}
		mu.Lock()
		block := crashed[id] && !crashSeen[id]
		if block {
			crashSeen[id] = true
		}
		mu.Unlock()
		if block {
			started <- id
			<-r.Context().Done()
			return
		}
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if n%5 == 0 {
			limited.Add(1)
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"fixture rate limit","type":"rate_limit","code":"rate_limit"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"}}],"usage":{"total_tokens":10}}`)
	}))
	defer srv.Close()
	spawn := func(batches []string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSharedQuotaProcessHelper$", "-test.v")
		cmd.Env = append(os.Environ(), "ARGUS_QUOTA_HELPER=1", "ARGUS_QUOTA_DSN="+u.String(), "ARGUS_QUOTA_HTTP="+srv.URL, "ARGUS_QUOTA_BATCHES="+strings.Join(batches, ","))
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return cmd
	}
	killed := map[string]time.Time{}
	for _, id := range ids[:2] {
		victim := spawn([]string{id})
		select {
		case got := <-started:
			if got != id {
				t.Fatal(got)
			}
		case <-time.After(5 * time.Second):
			victim.Process.Kill()
			t.Fatal("victim did not reach physical HTTP")
		}
		killed[id] = time.Now()
		if e = victim.Process.Kill(); e != nil {
			t.Fatal(e)
		}
		_ = victim.Wait()
	}
	// Both independent workers receive every task, including duplicate deliveries.
	workers := []*exec.Cmd{spawn(append(ids, ids...)), spawn(append(ids, ids...))}
	recoveries := []float64{}
	pending := map[string]bool{ids[0]: true, ids[1]: true}
	deadline := time.Now().Add(45 * time.Second)
	for len(pending) > 0 {
		for id := range pending {
			var count int
			if e = db.QueryRow(`SELECT count(*) FROM ai_diagnoses WHERE batch_id=$1 AND status='completed'`, id).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if count > 0 {
				recoveries = append(recoveries, float64(time.Since(killed[id]).Microseconds())/1000)
				delete(pending, id)
			}
		}
		if time.Now().After(deadline) {
			for _, w := range workers {
				w.Process.Kill()
			}
			t.Fatal("takeover deadline exceeded")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, w := range workers {
		if e = w.Wait(); e != nil {
			t.Fatal("worker failed", e)
		}
	}
	var completed, distinct, events, checkpointCount int
	if e = db.QueryRow(`SELECT count(*),count(distinct batch_id) FROM ai_diagnoses WHERE status='completed'`).Scan(&completed, &distinct); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM event_outbox WHERE event_type='DiagnosisCompleted'`).Scan(&events); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM investigation_checkpoints`).Scan(&checkpointCount); e != nil {
		t.Fatal(e)
	}
	if completed != len(ids) || distinct != len(ids) || events != len(ids) || checkpointCount != len(ids) || peak.Load() > 2 || limited.Load() == 0 || calls.Load() != int64(len(ids)+2)+limited.Load() {
		t.Fatalf("completed=%d distinct=%d outbox=%d checkpoints=%d peak=%d 429=%d", completed, distinct, events, checkpointCount, peak.Load(), limited.Load())
	}
	sort.Float64s(recoveries)
	evidence := map[string]interface{}{"provider": "local HTTP fixture; no paid model quality/cost claim", "independent_worker_processes": 2, "sigkill_victims": 2, "tasks": len(ids), "initial_scheduled_deliveries": len(ids)*4 + 2, "completed": completed, "lost_final_results": len(ids) - distinct, "duplicate_final_results": completed - distinct, "completion_outbox_events": events, "checkpoints": checkpointCount, "actual_http_calls": calls.Load(), "http_429": limited.Load(), "shared_peak_http": peak.Load(), "shared_capacity": 2, "recovery_ms": recoveries, "recovery_p95_ms": recoveries[len(recoveries)-1], "recovery_sample_size": len(recoveries), "request_bound_ms": 2000, "crash_grace_ms": 200, "task_lease_ms": 30000}
	data, _ := json.MarshalIndent(evidence, "", "  ")
	t.Log(string(data))
	if out := os.Getenv("ARGUS_LOAD_REPORT_DIR"); out != "" {
		if e = os.MkdirAll(out, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(out, "shared-quota-multiprocess.json"), data, 0644); e != nil {
			t.Fatal(e)
		}
	}
}

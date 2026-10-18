package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/google/uuid"
	coreapp "github.com/xuewentao/argus-ota-platform/internal/application"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/processing"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	app "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	aipg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

// Controlled embeddings make cosine distances reproducible; PostgreSQL vector
// ordering, actual CSV processing, diagnosis execution and persistence are real.
type routingEmbedder struct {
	score float64
	calls atomic.Int32
	fail  bool
}

func (e *routingEmbedder) EmbedStrings(_ context.Context, _ []string, _ ...embedding.Option) ([][]float64, error) {
	e.calls.Add(1)
	if e.fail {
		return nil, fmt.Errorf("controlled embedding outage")
	}
	v := make([]float64, 1536)
	v[0] = 1
	if e.score > 0 {
		v[0] = e.score
		v[1] = math.Sqrt(1 - e.score*e.score)
	}
	return [][]float64{v}, nil
}
func routingVector(score float64) string {
	values := make([]string, 1536)
	for i := range values {
		values[i] = "0"
	}
	values[0] = fmt.Sprint(score)
	values[1] = fmt.Sprint(math.Sqrt(1 - score*score))
	return "[" + strings.Join(values, ",") + "]"
}
func TestConfidenceRoutingE2E(t *testing.T) {
	for _, tc := range []struct {
		name, path                   string
		scores                       []float64
		conflict, unverified, outage bool
		calls                        int
	}{
		{name: "deterministic", path: "deterministic", calls: 0},
		{name: "semantic_agreement", path: "semantic", scores: []float64{.96, .95, .94, .8}, calls: 1},
		{name: "summary_invalid", path: "investigation", scores: []float64{.96, .95, .94}, calls: 3},
		{name: "novel", path: "investigation", scores: []float64{.6, .59, .58}, calls: 2},
		{name: "gray_zone", path: "investigation", scores: []float64{.8, .79, .78}, calls: 2},
		{name: "conflict", path: "investigation", scores: []float64{.96, .95, .94}, conflict: true, calls: 2},
		{name: "unverified", path: "investigation", scores: []float64{.96, .95, .94}, unverified: true, calls: 2},
		{name: "retrieval_outage", path: "investigation", outage: true, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.OpenDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Setenv("AI_RUNNER", "hybrid")
			t.Setenv("AI_HYBRID_RULES", "")
			t.Setenv("AI_HYBRID_THRESHOLDS", "")
			id, fid := uuid.New(), uuid.New()
			if _, err := db.Exec(`INSERT INTO batches(id,vehicle_id,vin,status,total_files,expected_worker_count) VALUES($1,'fixture','test','uploaded',1,1)`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO files(id,batch_id,filename,original_filename,file_size,minio_path) VALUES($1,$2,'ota.csv','ota.csv',100,'ota.csv')`, fid, id); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "csv", "ota_checksum.csv"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "deterministic" {
				raw = []byte(strings.ReplaceAll(string(raw), "OTA_CHECKSUM_MISMATCH", "UNKNOWN_INTERNAL_ERROR"))
			}
			processor := processing.Processor{DB: db, Python: processing.Python{Script: filepath.Join("..", "..", "..", "..", "workers", "python-aggregator", "regression.py")}, Fetch: func(context.Context, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(string(raw))), nil
			}}
			if err = processor.ParseBatch(ctx, id); err != nil {
				t.Fatal(err)
			}
			orchestrator := coreapp.NewOrchestrateService(corepg.NewPostgresBatchRepository(db), nil, nil)
			event, _ := json.Marshal(map[string]string{"event_type": "FileParsed", "batch_id": id.String(), "file_id": fid.String()})
			if err = orchestrator.HandleMessage(ctx, event); err != nil {
				t.Fatal(err)
			}
			if err = processor.AggregateBatch(ctx, id); err != nil {
				t.Fatal(err)
			}
			event, _ = json.Marshal(map[string]string{"event_type": "GatheringCompleted", "batch_id": id.String()})
			if err = orchestrator.HandleMessage(ctx, event); err != nil {
				t.Fatal(err)
			}
			repo := aipg.NewPostgresDiagnoseRepository(db)
			for i, score := range tc.scores {
				batch := uuid.New()
				if _, err = db.Exec(`INSERT INTO batches(id,vehicle_id,vin,status,total_files,expected_worker_count) VALUES($1,'history','test','completed',1,1)`, batch); err != nil {
					t.Fatal(err)
				}
				cause := "connector"
				if (tc.conflict && i == 1) || i == 3 {
					cause = "network"
				}
				now := time.Now()
				d := &domain.Diagnosis{ID: uuid.New(), BatchID: batch, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "Historical controlled diagnosis " + cause, AggregatedData: map[string]interface{}{"root_cause_id": cause, "case_verified": !tc.unverified, "case_module": "ota-downloader"}, TopErrorCodes: []string{"UNKNOWN_INTERNAL_ERROR"}, Model: "controlled-history", Recommendations: []string{}, CreatedAt: now, UpdatedAt: now}
				if err = repo.Save(ctx, d); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE ai_diagnoses SET embedding=$1::vector WHERE id=$2`, routingVector(score), d.ID); err != nil {
					t.Fatal(err)
				}
				if !tc.unverified {
					promoted, e := pgvector.NewPgvectorRetriever(db, &routingEmbedder{score: score})
					if e != nil {
						t.Fatal(e)
					}
					if e = promoted.PromoteCase(ctx, d.ID, cause, "ota-downloader", "", "e2e-operator"); e != nil {
						t.Fatal(e)
					}
				}

			}
			embed := &routingEmbedder{fail: tc.outage}
			retriever, err := pgvector.NewPgvectorRetriever(db, embed)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var req struct {
					Messages []struct{ Role, Content string }
				}
				json.NewDecoder(r.Body).Decode(&req)
				fixed := len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "Summarize the supplied observations")
				message := map[string]interface{}{"role": "assistant"}
				if !fixed && (n == 1 || (tc.name == "summary_invalid" && n == 2)) {
					message["content"] = ""
					message["tool_calls"] = []interface{}{
						map[string]interface{}{"id": "logs", "type": "function", "function": map[string]string{"name": "read_logs", "arguments": "{}"}},
						map[string]interface{}{"id": "stats", "type": "function", "function": map[string]string{"name": "error_statistics", "arguments": "{}"}},
						map[string]interface{}{"id": "plan", "type": "function", "function": map[string]string{"name": "update_investigation_plan", "arguments": `{"hypotheses":[{"id":"H1","description":"Observed failure; cause unproven","status":"supported","supports":["log:1"],"next_check":"Inspect upstream cause"}]}`}},
					}
				} else {
					stat := "stats:UNKNOWN_INTERNAL_ERROR"
					if fixed {
						stat = "stats:1"
					}
					content, _ := json.Marshal(map[string]interface{}{"hypothesis_id": "H1", "root_cause": "Observed failure; historical cause remains unproven", "suggestions": []string{"inspect"}, "severity": "high", "confidence": .1, "evidence_ids": []string{"log:1", stat}, "limitations": []string{"Controlled model fixture"}})
					message["content"] = string(content)
				}
				if fixed && tc.name == "summary_invalid" {
					message["content"] = "invalid JSON"
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "message": message, "finish_reason": "stop"}}, "usage": map[string]int{"total_tokens": 30}})
			}))
			defer server.Close()
			service, err := app.NewMultiAgentService(repo, retriever, nil, &llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL + "/"})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err = service.DiagnoseBatch(ctx, id.String()); err != nil {
					t.Fatal(err)
				}
			}
			if int(calls.Load()) != tc.calls {
				t.Fatalf("want %d HTTP calls got %d", tc.calls, calls.Load())
			}
			d, err := repo.FindByBatchID(ctx, id.String())
			if err != nil {
				t.Fatal(err)
			}
			decision, ok := d.AggregatedData["routing"].(map[string]interface{})
			if !ok || decision["path"] != tc.path {
				t.Fatalf("routing not persisted: %+v", d.AggregatedData)
			}
			report, err := corepg.NewPostgresReportRepository(db).FindByBatchID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			var exposed map[string]interface{}
			if json.Unmarshal(report.Routing, &exposed) != nil || exposed["path"] != tc.path {
				t.Fatalf("report routing lost %s", report.Routing)
			}
			var count int
			if err = db.QueryRow(`SELECT COUNT(*) FROM ai_diagnoses WHERE batch_id=$1`, id).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate diagnosis %d %v", count, err)
			}
			if tc.path == "deterministic" && embed.calls.Load() != 0 {
				t.Fatal("L1 must bypass embedding")
			}
			receipt := map[string]interface{}{"case": tc.name, "path": tc.path, "deliveries": 2, "diagnoses": count, "actual_http_calls": calls.Load(), "embedding_calls": embed.calls.Load(), "routing": decision, "scope": "actual CSV/PostgreSQL/pgvector/execution/persistence; controlled embeddings and scripted model, no diagnosis quality claim"}
			b, _ := json.MarshalIndent(receipt, "", "  ")
			t.Log(string(b))
			if dir := os.Getenv("ARGUS_LOAD_REPORT_DIR"); dir != "" {
				if err = os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, "confidence-routing-"+tc.name+".json"), b, 0644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

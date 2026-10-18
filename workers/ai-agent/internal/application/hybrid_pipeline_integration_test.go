package application_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	coreapp "github.com/xuewentao/argus-ota-platform/internal/application"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/processing"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	app "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	aipg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

func TestHybridUsesActuallyParsedOTAObservationsWithoutModel(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	id, fid := uuid.New(), uuid.New()
	t.Setenv("AI_RUNNER", "hybrid")
	if _, e := db.Exec(`INSERT INTO batches(id,vehicle_id,vin,status,total_files,expected_worker_count) VALUES($1,'public-fixture','test','uploaded',1,1)`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO files(id,batch_id,filename,original_filename,file_size,minio_path) VALUES($1,$2,'ota.csv','ota.csv',100,'ota.csv')`, fid, id); e != nil {
		t.Fatal(e)
	}
	processor := processing.Processor{DB: db, Python: processing.Python{Script: filepath.Join("..", "..", "..", "..", "workers", "python-aggregator", "regression.py")}, Fetch: func(context.Context, string) (io.ReadCloser, error) {
		return os.Open(filepath.Join("..", "..", "..", "..", "testdata", "csv", "ota_checksum.csv"))
	}}
	if e := processor.ParseBatch(ctx, id); e != nil {
		t.Fatal(e)
	}
	orchestrator := coreapp.NewOrchestrateService(corepg.NewPostgresBatchRepository(db), nil, nil)
	event, _ := json.Marshal(map[string]string{"event_type": "FileParsed", "batch_id": id.String(), "file_id": fid.String()})
	if e := orchestrator.HandleMessage(ctx, event); e != nil {
		t.Fatal(e)
	}
	if e := processor.AggregateBatch(ctx, id); e != nil {
		t.Fatal(e)
	}
	event, _ = json.Marshal(map[string]string{"event_type": "GatheringCompleted", "batch_id": id.String()})
	if e := orchestrator.HandleMessage(ctx, event); e != nil {
		t.Fatal(e)
	}
	repo := aipg.NewPostgresDiagnoseRepository(db)
	data, e := repo.GetAggregatedData(ctx, id.String())
	if e != nil {
		t.Fatal(e)
	}
	if data.ErrorCodeStats["OTA_CHECKSUM_MISMATCH"] != 2 || data.StatsGrain != "log_occurrence" {
		t.Fatalf("wrong real grain %+v", data)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "message": map[string]string{"role": "assistant", "content": `{"hypothesis_id":"H1","root_cause":"Observed checksum mismatch; upstream cause unproven","suggestions":["Check download artifact and manifest separately"],"severity":"high","confidence":0,"evidence_ids":["log:1","stats:1"]}`}}}, "usage": map[string]int{"total_tokens": 30}})
	}))
	defer server.Close()
	service, e := app.NewMultiAgentService(repo, nil, nil, &llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = service.DiagnoseBatch(ctx, id.String()); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("actual parsed route used %d model calls", calls.Load())
	}
	report, e := corepg.NewPostgresReportRepository(db).FindByBatchID(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if report.ResolutionStatus != "provisional" {
		t.Fatalf("resolution lost %+v", report)
	}
	output := map[string]interface{}{"input": "actual CSV parse products in PostgreSQL", "provider": "local scripted HTTP fixture; no root-cause quality claim", "error_occurrences": 2, "actual_http_calls": calls.Load(), "deliveries": 2, "resolution_status": report.ResolutionStatus, "duplicate_final_results": 0}
	raw, _ := json.MarshalIndent(output, "", "  ")
	t.Log(string(raw))
	if out := os.Getenv("ARGUS_LOAD_REPORT_DIR"); out != "" {
		os.MkdirAll(out, 0755)
		if e = os.WriteFile(filepath.Join(out, "actual-hybrid-pipeline.json"), raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
}

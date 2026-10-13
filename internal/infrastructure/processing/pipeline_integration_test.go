package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
)

func TestRealCSVProcessingDurableRegressionAndReplay(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	id := uuid.New()
	fid := uuid.New()
	_, e := db.Exec(`INSERT INTO batches(id,vehicle_id,vin,status,total_files,expected_worker_count) VALUES($1,'V1','public-fixture','uploaded',1,1)`, id)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO files(id,batch_id,filename,original_filename,file_size,file_type,minio_path) VALUES($1,$2,'metrics.csv','metrics.csv',100,'csv','batch/metrics.csv')`, fid, id)
	if e != nil {
		t.Fatal(e)
	}
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
	fetches := 0
	p := Processor{DB: db, Python: Python{Script: filepath.Join("..", "..", "..", "workers", "python-aggregator", "regression.py"), Baseline: "v1", Candidate: "v2"}, Fetch: func(ctx context.Context, key string) (io.ReadCloser, error) {
		fetches++
		return io.NopCloser(strings.NewReader(content)), nil
	}}
	for i := 0; i < 2; i++ {
		if e = p.ParseBatch(ctx, id); e != nil {
			t.Fatal(e)
		}
	}
	if fetches != 1 {
		t.Fatalf("duplicate delivery fetched %d times", fetches)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM logs WHERE batch_id=$1`, id).Scan(&count)
	if count != 20 {
		t.Fatalf("log count %d", count)
	}
	service := application.NewOrchestrateService(corepg.NewPostgresBatchRepository(db), nil, nil)
	payload, _ := json.Marshal(map[string]string{"event_type": "FileParsed", "batch_id": id.String(), "file_id": fid.String()})
	if e = service.HandleMessage(ctx, payload); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = p.AggregateBatch(ctx, id); e != nil {
			t.Fatal(e)
		}
	}
	var raw []byte
	if e = db.QueryRow(`SELECT report FROM version_regression_reports WHERE batch_id=$1`, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var result struct {
		Status     string            `json:"status"`
		Candidates []json.RawMessage `json:"candidates"`
		Matched    int               `json:"matched_samples"`
	}
	json.Unmarshal(raw, &result)
	if result.Status != "comparable" || len(result.Candidates) != 1 || result.Matched != 20 {
		t.Fatalf("wrong regression %s", raw)
	}
	db.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE batch_id=$1 AND event_type='GatheringCompleted'`, id).Scan(&count)
	if count != 1 {
		t.Fatalf("duplicate completions %d", count)
	}
	repo := corepg.NewPostgresReportRepository(db)
	report, e := repo.FindByBatchID(ctx, id)
	if e != nil || len(report.VersionRegression) == 0 {
		t.Fatalf("query omitted regression %v", e)
	}
	if out := os.Getenv("ARGUS_LOAD_REPORT_DIR"); out != "" {
		os.MkdirAll(out, 0755)
		if e = os.WriteFile(filepath.Join(out, "version-regression-pipeline.json"), raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("actual_csv_rows=20 duplicate_results=0 duplicate_completions=0 parser_fetches=%d matched_samples=%d", fetches, result.Matched)
}

func TestPublicRecordPerFileVersionProvenance(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip("python unavailable")
	}
	cmd := exec.Command("python3", "-c", "import cyber_record")
	if e := cmd.Run(); e != nil {
		t.Skip("install optional record parser")
	}
	db := testutil.OpenDB(t)
	ctx := context.Background()
	id := uuid.New()
	if _, e := db.Exec(`INSERT INTO batches(id,vehicle_id,vin,status,total_files,expected_worker_count) VALUES($1,'public','fixture','uploaded',2,1)`, id); e != nil {
		t.Fatal(e)
	}
	versions := []string{"v1", "v2"}
	ids := []uuid.UUID{}
	for _, v := range versions {
		f := uuid.New()
		ids = append(ids, f)
		metadata := fmt.Sprintf(`{"record_format":"apollo_cyber","version":%q,"vehicle_id":"public","vehicle_class":"unknown","replay_id":"same-public-example","workload":"caller-labelled-simcontrol"}`, v)
		if _, e := db.Exec(`INSERT INTO files(id,batch_id,filename,original_filename,file_size,minio_path,metadata) VALUES($1,$2,$3,'example.record.00000',489010,$3,$4)`, f, id, f.String(), metadata); e != nil {
			t.Fatal(e)
		}
	}
	fixture := filepath.Join("..", "..", "..", "testdata", "public-record", "example.record.00000")
	p := Processor{DB: db, Python: Python{Script: filepath.Join("..", "..", "..", "workers", "python-aggregator", "regression.py"), Baseline: "v1", Candidate: "v2"}, Fetch: func(context.Context, string) (io.ReadCloser, error) { return os.Open(fixture) }}
	if e := p.ParseBatch(ctx, id); e != nil {
		t.Fatal(e)
	}
	service := application.NewOrchestrateService(corepg.NewPostgresBatchRepository(db), nil, nil)
	for _, f := range ids {
		body, _ := json.Marshal(map[string]string{"event_type": "FileParsed", "batch_id": id.String(), "file_id": f.String()})
		if e := service.HandleMessage(ctx, body); e != nil {
			t.Fatal(e)
		}
	}
	if e := p.AggregateBatch(ctx, id); e != nil {
		t.Fatal(e)
	}
	var raw []byte
	if e := db.QueryRow(`SELECT report FROM version_regression_reports WHERE batch_id=$1`, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var result struct {
		Versions   []string          `json:"versions"`
		Samples    int               `json:"sample_count"`
		Candidates []json.RawMessage `json:"candidates"`
	}
	if e := json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Versions) != 2 || result.Samples != 68 || len(result.Candidates) != 0 {
		t.Fatalf("per-file provenance not preserved or metrics fabricated %s", raw)
	}
	t.Log("two actual public records decoded=68; per-file versions retained; CPU/memory/errors remain unobserved; metadata supplied for test, no measured OTA versions")
}

package application_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	coreapp "github.com/xuewentao/argus-ota-platform/internal/application"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	app "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	aipg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeepSeekInvestigationLive(t *testing.T) {
	if os.Getenv("ARGUS_LIVE_DEEPSEEK") != "1" {
		t.Skip("explicit opt-in required for paid live API test")
	}
	keyBytes, err := os.ReadFile(os.Getenv("DEEPSEEK_API_KEY_FILE"))
	if err != nil {
		t.Fatal("unable to read private API key file")
	}
	key := strings.TrimSpace(string(keyBytes))
	if key == "" {
		t.Fatal("API key file empty")
	}
	db := testutil.OpenDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	repo := corepg.NewPostgresBatchRepository(db)
	svc := coreapp.NewBatchService(repo, corepg.NewPostgresFileRepository(db), nil)
	b, err := svc.CreateBatch(ctx, "synthetic-live-test", "SYNTHETICVIN", 1)
	if err != nil {
		t.Fatal(err)
	}
	f := uuid.New()
	if err = svc.AddFile(ctx, b.ID, f, "synthetic.rec", 10, "test/synthetic"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE files SET error_code='E001' WHERE id=$1;`, f); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"SYNTHETIC TEST E001 CPU utilization=98%", "SYNTHETIC TEST CPU temperature=95C", "SYNTHETIC TEST fan speed=0 RPM; no recent firmware change"} {
		if _, err = db.ExecContext(ctx, `INSERT INTO logs(batch_id,file_id,log_content) VALUES($1,$2,$3)`, b.ID, f, line); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `UPDATE batches SET status='diagnosing',processed_files=total_files WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	diagnosisRepo := aipg.NewPostgresDiagnoseRepository(db)
	cfg := &llm.GLM4Config{APIKey: key, BaseURL: "https://api.deepseek.com", Model: "deepseek-flash"}
	service, err := app.NewMultiAgentService(diagnosisRepo, pgvector.NewTextRetriever(db), nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DiagnoseBatch(ctx, b.ID.String()); err != nil {
		t.Fatal(strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
	}
	saved, err := diagnosisRepo.FindByBatchID(ctx, b.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil || saved.Status != domain.DiagnosisStatusCompleted {
		t.Fatal("diagnosis not persisted")
	}
	relay := &liveCompletionRelay{service: coreapp.NewOrchestrateService(repo, nil, nil)}
	if _, err = corepg.DispatchOutbox(ctx, db, relay, 100); err != nil {
		t.Fatal(err)
	}
	report, err := corepg.NewPostgresReportRepository(db).FindByBatchID(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.DiagnosisResult == "" || len(report.InvestigationTrace) == 0 || len(report.Evidence) == 0 {
		t.Fatal("saved report missing tool trace/evidence")
	}
	var trace []domain.ToolObservation
	if err = json.Unmarshal(report.InvestigationTrace, &trace); err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, step := range trace {
		tools[step.Tool] = true
	}
	if !tools["read_logs"] || !tools["error_statistics"] {
		t.Fatal("model did not investigate via required tools")
	}
	if err = service.DiagnoseBatch(ctx, b.ID.String()); err != nil {
		t.Fatal(err)
	} // durable replay, no further model generation
	artifact := map[string]interface{}{"kind": "live_functional_test", "model": saved.Model, "batch_id": b.ID, "tokens_used": saved.TokensUsed, "diagnosis": saved.DiagnosisSummary, "tool_trace": trace, "evidence": json.RawMessage(report.Evidence), "limitations": json.RawMessage(report.DiagnosisLimitations), "quality_evaluation": "PENDING", "fixture": "synthetic CPU/fan fault; no private vehicle logs"}
	if path := os.Getenv("ARGUS_LIVE_REPORT_PATH"); path != "" {
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(artifact, "", "  ")
		if err = os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("live functional test passed: model=%s tools=%d tokens=%d; result, evidence and trace persisted; quality evaluation PENDING", saved.Model, len(trace), saved.TokensUsed)
}

type liveCompletionRelay struct{ service *coreapp.OrchestrateService }

func (r *liveCompletionRelay) PublishEvents(ctx context.Context, events []core.DomainEvent) error {
	for _, event := range events {
		body, err := core.MarshalEvent(event)
		if err != nil {
			return err
		}
		if err = r.service.HandleMessage(ctx, body); err != nil {
			return err
		}
	}
	return nil
}
func (r *liveCompletionRelay) Close() error { return nil }

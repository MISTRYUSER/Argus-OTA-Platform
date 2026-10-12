package postgres_test

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	coreapp "github.com/xuewentao/argus-ota-platform/internal/application"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	pg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiagnosisPersistenceAndReport(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d := &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "Connector loose", TopErrorCodes: []string{"E001"}, Recommendations: []string{"Inspect connector"}, Confidence: 0.85, Model: "fixture", DiagnosedAt: &now, CreatedAt: now, UpdatedAt: now}
	repo := pg.NewPostgresDiagnoseRepository(db)
	if err := repo.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByBatchID(ctx, b.ID.String())
	if err != nil || got == nil {
		t.Fatalf("read result=%v err=%v", got, err)
	}
	if got.DiagnosisSummary != "Connector loose" {
		t.Fatal(got)
	}
	report, err := corepg.NewPostgresReportRepository(db).FindByBatchID(ctx, b.ID)
	if err != nil || report == nil {
		t.Fatalf("report=%v err=%v", report, err)
	}
	if report.DiagnosisResult != "Connector loose" || report.DiagnosisID == nil || *report.DiagnosisID != got.ID {
		t.Fatal(report)
	}
}

func TestDiagnosisAndOutboxRollbackTogether(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`CREATE FUNCTION reject_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture outage'; END; $$; CREATE TRIGGER reject_outbox BEFORE INSERT ON event_outbox FOR EACH ROW EXECUTE FUNCTION reject_outbox()`)
	if err != nil {
		t.Fatal(err)
	}
	repo := pg.NewPostgresDiagnoseRepository(db).(domain.IdempotentDiagnosisRepository)
	generate := func() (*domain.Diagnosis, error) {
		now := time.Now()
		return &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "fixture", Confidence: .8, Model: "fixture", DiagnosedAt: &now, CreatedAt: now, UpdatedAt: now}, nil
	}
	if _, err = repo.GetOrCreate(ctx, b.ID.String(), generate); err == nil {
		t.Fatal("expected outbox failure")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM ai_diagnoses`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("diagnosis escaped failed transaction")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_outbox ON event_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetOrCreate(ctx, b.ID.String(), generate); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalUUIDAliasesShareDiagnosisLock(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	repo := pg.NewPostgresDiagnoseRepository(db).(domain.IdempotentDiagnosisRepository)
	var calls atomic.Int32
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, id := range []string{b.ID.String(), strings.ToUpper(b.ID.String())} {
		go func(id string) {
			<-start
			_, err := repo.GetOrCreate(ctx, id, func() (*domain.Diagnosis, error) {
				calls.Add(1)
				time.Sleep(60 * time.Millisecond)
				now := time.Now()
				return &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "fixture", Model: "fixture", CreatedAt: now, UpdatedAt: now}, nil
			})
			errs <- err
		}(id)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("model generated %d times for one UUID", calls.Load())
	}
}

func TestTerminalFailureIsDurableAndPreventsRegeneration(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.Status = core.BatchStatusDiagnosing
	b.ClearEvents()
	repo := corepg.NewPostgresBatchRepository(db)
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	recorder := r.(interface {
		RecordFailure(context.Context, string, error) error
	})
	if err := recorder.RecordFailure(ctx, b.ID.String(), fmt.Errorf("invalid model output")); err != nil {
		t.Fatal(err)
	}
	stored, err := r.FindByBatchID(ctx, b.ID.String())
	if err != nil || stored == nil || stored.Status != domain.DiagnosisStatusFailed {
		t.Fatalf("stored=%v err=%v", stored, err)
	}
	called := false
	if _, err = r.(domain.IdempotentDiagnosisRepository).GetOrCreate(ctx, b.ID.String(), func() (*domain.Diagnosis, error) { called = true; return nil, nil }); err == nil || called {
		t.Fatal("terminal failure restarted generation")
	}
	relay := &failureRelay{app: coreapp.NewOrchestrateService(repo, nil, nil)}
	if _, err = corepg.DispatchOutbox(ctx, db, relay, 10); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.FindByID(ctx, b.ID)
	if got.Status != core.BatchStatusFailed {
		t.Fatal(got.Status)
	}
}

type failureRelay struct{ app *coreapp.OrchestrateService }

func (r *failureRelay) PublishEvents(ctx context.Context, events []core.DomainEvent) error {
	for _, e := range events {
		b, err := core.MarshalEvent(e)
		if err != nil {
			return err
		}
		if err = r.app.HandleMessage(ctx, b); err != nil {
			return err
		}
	}
	return nil
}
func (r *failureRelay) Close() error { return nil }

func TestFailurePersistenceAcceptsUnicodeAndNUL(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := corepg.NewPostgresBatchRepository(db)
	r := pg.NewPostgresDiagnoseRepository(db).(interface {
		RecordFailure(context.Context, string, error) error
	})
	for _, message := range []string{"x" + strings.Repeat("中", 600), "model evidence contains\x00NUL"} {
		b, _ := core.NewBatch("fixture", "VIN", 1)
		b.ClearEvents()
		if err := repo.Save(ctx, b); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordFailure(ctx, b.ID.String(), fmt.Errorf("%s", message)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresCheckpointAndArtifactIsolation(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := corepg.NewPostgresBatchRepository(db)
	a, _ := core.NewBatch("fixture", "VIN", 1)
	a.ClearEvents()
	b, _ := core.NewBatch("other", "VIN2", 1)
	b.ClearEvents()
	if err := repo.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	check := r.(domain.InvestigationCheckpointStore)
	if err := check.SaveInvestigationCheckpoint(ctx, a.ID.String(), []byte(`{"phase":"plan"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := check.LoadInvestigationCheckpoint(ctx, a.ID.String())
	if err != nil || string(data) != `{"phase":"plan"}` {
		t.Fatalf("snapshot=%s err=%v", data, err)
	}
	store := r.(domain.InvestigationArtifactStore)
	id, err := store.PutInvestigationArtifact(ctx, a.ID.String(), []byte(`{"evidence":"full"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetInvestigationArtifact(ctx, b.ID.String(), id); err == nil {
		t.Fatal("artifact crossed batch scope")
	}
}

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	pg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"strings"
	"testing"
)

type collectPublisher struct{ events []domain.DomainEvent }

func (p *collectPublisher) PublishEvents(_ context.Context, e []domain.DomainEvent) error {
	p.events = append(p.events, e...)
	return nil
}
func (p *collectPublisher) Close() error { return nil }
func TestUploadParseAggregatePipeline(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := pg.NewPostgresBatchRepository(db)
	svc := application.NewBatchService(repo, pg.NewPostgresFileRepository(db), nil)
	b, err := svc.CreateBatch(ctx, "fixture", "VIN", 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for _, id := range ids {
		if err = svc.AddFile(ctx, b.ID, id, "test.rec", 12, "fixture/object"); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.TransitionBatchStatus(ctx, b.ID, domain.BatchStatusUploaded); err != nil {
		t.Fatal(err)
	}
	orchestrator := application.NewOrchestrateService(repo, nil, nil)
	publisher := &collectPublisher{}
	dispatch := func() {
		t.Helper()
		publisher.events = nil
		if _, err := pg.DispatchOutbox(ctx, db, publisher, 100); err != nil {
			t.Fatal(err)
		}
		for _, e := range publisher.events {
			body, err := domain.MarshalEvent(e)
			if err != nil {
				t.Fatal(err)
			}
			if err = orchestrator.HandleMessage(ctx, body); err != nil {
				t.Fatal(err)
			}
		}
	}
	dispatch()
	if err = pg.MockParseBatch(ctx, db, b.ID); err != nil {
		t.Fatal(err)
	}
	dispatch()
	got, err := repo.FindByID(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BatchStatusGathering || got.ProcessedFiles != 2 {
		t.Fatalf("batch=%+v", got)
	}
	if err = pg.MockAggregateBatch(ctx, db, b.ID); err != nil {
		t.Fatal(err)
	}
	dispatch()
	got, _ = repo.FindByID(ctx, b.ID)
	if got.Status != domain.BatchStatusDiagnosing {
		t.Fatal(got.Status)
	}
	// Repeat the same real file completion; it must never increment or regress the batch.
	body, _ := json.Marshal(map[string]string{"event_type": "FileParsed", "batch_id": b.ID.String(), "file_id": ids[0].String()})
	if err = orchestrator.HandleMessage(ctx, body); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.FindByID(ctx, b.ID)
	if got.ProcessedFiles != 2 || got.Status != domain.BatchStatusDiagnosing {
		t.Fatal(got)
	}
}

func TestCompensationRequeuesPublishedRequest(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := pg.NewPostgresBatchRepository(db)
	b, _ := domain.NewBatch("fixture", "VIN", 1)
	b.Status = domain.BatchStatusDiagnosing
	b.TotalFiles = 1
	b.ProcessedFiles = 1
	b.ClearEvents()
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	svc := application.NewOrchestrateService(repo, nil, nil)
	if err := svc.HandleStuckBatch(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.DispatchOutbox(ctx, db, &collectPublisher{}, 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleStuckBatch(ctx, b); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE event_type='DiagnosisRequested' AND published_at IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d; recovery request was lost", pending)
	}
}

type failingPublisher struct{}

func (*failingPublisher) PublishEvents(context.Context, []domain.DomainEvent) error {
	return fmt.Errorf("broker unavailable")
}
func (*failingPublisher) Close() error { return nil }
func TestOutboxFailureRetainsEventForRetry(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := pg.NewPostgresBatchRepository(db)
	b, _ := domain.NewBatch("fixture", "VIN", 1)
	b.Status = domain.BatchStatusDiagnosing
	b.ClearEvents()
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	svc := application.NewOrchestrateService(repo, nil, nil)
	if err := svc.HandleStuckBatch(ctx, b); err != nil {
		t.Fatal(err)
	}
	if n, err := pg.DispatchOutbox(ctx, db, &failingPublisher{}, 10); err == nil || n != 0 {
		t.Fatalf("sent=%d err=%v", n, err)
	}
	sink := &collectPublisher{}
	if n, err := pg.DispatchOutbox(ctx, db, sink, 10); err != nil || n != 1 {
		t.Fatalf("retry sent=%d err=%v", n, err)
	}
	if n, err := pg.DispatchOutbox(ctx, db, sink, 10); err != nil || n != 0 {
		t.Fatalf("duplicate sent=%d err=%v", n, err)
	}
}
func TestConcurrentUploadsPreserveFileCount(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := pg.NewPostgresBatchRepository(db)
	svc := application.NewBatchService(repo, pg.NewPostgresFileRepository(db), nil)
	b, err := svc.CreateBatch(ctx, "fixture", "VIN", 1)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() { errs <- svc.AddFile(ctx, b.ID, uuid.New(), "fixture.rec", 12, "fixture/object") }()
	}
	for i := 0; i < 12; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.FindByID(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalFiles != 12 {
		t.Fatalf("files=%d", got.TotalFiles)
	}
}

func TestCompletionRejectsMissingOrForeignDiagnosis(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	repo := pg.NewPostgresBatchRepository(db)
	b, _ := domain.NewBatch("fixture", "VIN", 1)
	b.Status = domain.BatchStatusDiagnosing
	b.ClearEvents()
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	other, _ := domain.NewBatch("other", "OTHER", 1)
	other.ClearEvents()
	if err := repo.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	foreign := uuid.New()
	if _, err := db.Exec(`INSERT INTO ai_diagnoses(id,batch_id,model_name,diagnosis_summary,status) VALUES($1,$2,'fixture','other result','completed')`, foreign, other.ID); err != nil {
		t.Fatal(err)
	}
	svc := application.NewOrchestrateService(repo, nil, nil)
	for _, id := range []uuid.UUID{uuid.New(), foreign} {
		body, _ := json.Marshal(map[string]string{"event_type": "DiagnosisCompleted", "batch_id": b.ID.String(), "diagnosis_id": id.String()})
		if err := svc.HandleMessage(ctx, body); err == nil {
			t.Fatalf("unrelated diagnosis %s accepted", id)
		}
		got, _ := repo.FindByID(ctx, b.ID)
		if got.Status != domain.BatchStatusDiagnosing {
			t.Fatal("batch incorrectly completed")
		}
	}
}

func TestDeadLetterStoreRetainsEntireBinaryPayload(t *testing.T) {
	db := testutil.OpenDB(t)
	payload := bytes.Repeat([]byte{0, 255, 1}, 300000)
	reference, err := pg.DeadLetterStore(db)(context.Background(), "fixture", 0, 1, payload, "x\x00"+strings.Repeat("中", 600))
	if err != nil {
		t.Fatal(err)
	}
	var saved []byte
	if err = db.QueryRow(`SELECT payload FROM dead_letter_messages WHERE id=$1`, strings.TrimPrefix(reference, "postgres:dead_letter_messages:")).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, payload) {
		t.Fatal("dead letter payload was truncated")
	}
}

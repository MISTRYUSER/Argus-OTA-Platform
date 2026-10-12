package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	pg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
	"testing"
	"time"
)

func TestGenerationDoesNotHoldOpenTransaction(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	repo := pg.NewPostgresDiagnoseRepository(db).(domain.IdempotentDiagnosisRepository)
	_, err := repo.GetOrCreate(ctx, b.ID.String(), func() (*domain.Diagnosis, error) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=current_setting('application_name') AND state='idle in transaction'`).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%d database transactions held across model execution", count)
		}
		now := time.Now()
		return &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "fixture", Model: "fixture", CreatedAt: now, UpdatedAt: now}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpiredOwnerCannotCheckpointOrCommit(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	repo := corepg.NewPostgresBatchRepository(db)
	if err := repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	leased := r.(domain.LeasedDiagnosisRepository)
	var oldCtx context.Context
	_, err := leased.ExecuteOnce(ctx, b.ID.String(), func(runCtx context.Context) (*domain.Diagnosis, error) {
		oldCtx = runCtx
		if _, err := db.Exec(`UPDATE investigation_executions SET owner=$2,generation=generation+1,lease_until=NOW()+INTERVAL '30 seconds' WHERE batch_id=$1`, b.ID, uuid.New()); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		return &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "stale", Model: "fixture", CreatedAt: now, UpdatedAt: now}, nil
	})
	var deferred *core.DeferredTaskError
	if !errors.As(err, &deferred) {
		t.Fatalf("stale commit allowed: %v", err)
	}
	err = r.(domain.InvestigationCheckpointStore).SaveInvestigationCheckpoint(context.WithoutCancel(oldCtx), b.ID.String(), []byte(`{"stale":true}`))
	if !errors.As(err, &deferred) {
		t.Fatalf("stale checkpoint allowed: %v", err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM ai_diagnoses`).Scan(&count)
	if count != 0 {
		t.Fatal("stale result persisted")
	}
}
func TestFailureRecorderCannotClobberActiveExecution(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	recorder := r.(interface {
		RecordFailure(context.Context, string, error) error
	})
	_, err := r.(domain.LeasedDiagnosisRepository).ExecuteOnce(ctx, b.ID.String(), func(runCtx context.Context) (*domain.Diagnosis, error) {
		err := recorder.RecordFailure(ctx, b.ID.String(), fmt.Errorf("unrelated failure"))
		var deferred *core.DeferredTaskError
		if !errors.As(err, &deferred) {
			t.Fatalf("active run clobbered: %v", err)
		}
		now := time.Now()
		return &domain.Diagnosis{ID: uuid.New(), BatchID: b.ID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: "valid", Model: "fixture", CreatedAt: now, UpdatedAt: now}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSupersededErrorCannotFailNewGeneration(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	_, err := r.(domain.LeasedDiagnosisRepository).ExecuteOnce(ctx, b.ID.String(), func(context.Context) (*domain.Diagnosis, error) {
		_, e := db.Exec(`UPDATE investigation_executions SET owner=NULL,generation=generation+1,lease_until=NULL WHERE batch_id=$1`, b.ID)
		if e != nil {
			t.Fatal(e)
		}
		return nil, domain.ErrInvalidDiagnosis
	})
	var deferred *core.DeferredTaskError
	if !errors.As(err, &deferred) {
		t.Fatalf("stale permanent error escaped fencing: %v", err)
	}
}
func TestTerminalFailureCommitsBeforeOwnerRelease(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db)
	_, err := r.(domain.LeasedDiagnosisRepository).ExecuteOnce(ctx, b.ID.String(), func(context.Context) (*domain.Diagnosis, error) {
		return nil, &core.PermanentTaskError{Err: domain.ErrInvalidDiagnosis}
	})
	if err == nil {
		t.Fatal("failure expected")
	}
	saved, _ := r.FindByBatchID(ctx, b.ID.String())
	if saved == nil || saved.Status != domain.DiagnosisStatusFailed {
		t.Fatal("terminal result not saved before releasing lease")
	}
}

func TestOlderQuarantineCannotFailReleasedNewerGeneration(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	b, _ := core.NewBatch("fixture", "VIN", 1)
	b.ClearEvents()
	if err := corepg.NewPostgresBatchRepository(db).Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	oldLease := domain.ExecutionLease{BatchID: b.ID, Owner: uuid.New(), Generation: 1}
	if _, err := db.Exec(`INSERT INTO investigation_executions(batch_id,generation) VALUES($1,2)`, b.ID); err != nil {
		t.Fatal(err)
	}
	r := pg.NewPostgresDiagnoseRepository(db).(interface {
		RecordFailure(context.Context, string, error) error
	})
	if err := r.RecordFailure(ctx, b.ID.String(), &domain.ExecutionFailure{Lease: oldLease, Err: domain.ErrInvalidDiagnosis}); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM ai_diagnoses`).Scan(&count)
	if count != 0 {
		t.Fatal("old failure clobbered newer released generation")
	}
}

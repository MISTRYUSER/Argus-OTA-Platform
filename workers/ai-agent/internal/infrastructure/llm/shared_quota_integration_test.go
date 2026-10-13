package llm

import (
	"context"
	"errors"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"testing"
	"time"
)

func TestSharedQuotaLiveSessionCannotExpire(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	q, e := NewPostgresQuota(ctx, db, "live", 1, 10*time.Millisecond, 10*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	release, e := q.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(30 * time.Millisecond)
	if _, e = q.Acquire(ctx); !errors.Is(e, ErrModelQueueFull) {
		release()
		t.Fatalf("live request expired: %v", e)
	}
	release()
	release, e = q.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	release()
	if _, e = NewPostgresQuota(ctx, db, "live", 2, 10*time.Millisecond, 10*time.Millisecond); e == nil {
		t.Fatal("inconsistent shared capacity allowed")
	}
	if _, e = db.Exec(`UPDATE model_quota_scopes SET capacity=2 WHERE scope='live'`); e != nil {
		t.Fatal(e)
	}
	if _, e = q.Acquire(ctx); !errors.Is(e, ErrModelQueueFull) {
		t.Fatalf("policy change must fail closed: %v", e)
	}
}
func TestSharedQuotaDatabaseFailureDoesNotCallModel(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	q, e := NewPostgresQuota(ctx, db, "closed", 1, time.Second, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	f := &gateFixture{}
	g, e := NewGovernedModel(f, GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond, RequestTimeout: time.Second, Shared: q})
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.Generate(ctx, nil)
	if !errors.Is(e, ErrModelQueueFull) || f.calls != 0 || g.Snapshot().Attempts != 0 {
		t.Fatalf("err=%v calls=%d stats=%+v", e, f.calls, g.Snapshot())
	}
}

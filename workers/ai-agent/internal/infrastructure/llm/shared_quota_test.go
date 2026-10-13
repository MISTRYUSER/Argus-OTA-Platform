package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type deniedPermit struct{ calls int }

func (d *deniedPermit) Acquire(context.Context) (func(), error) {
	d.calls++
	return nil, ErrModelQueueFull
}
func TestSharedRejectionDoesNotInvokeModel(t *testing.T) {
	d := &deniedPermit{}
	f := &gateFixture{}
	g, e := NewGovernedModel(f, GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond, Shared: d, RequestTimeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.Generate(context.Background(), nil)
	if !errors.Is(e, ErrModelQueueFull) || f.calls != 0 || g.Snapshot().Attempts != 0 {
		t.Fatalf("err=%v calls=%d stats=%+v", e, f.calls, g.Snapshot())
	}
}

type countedPermit struct{ acquired, released int }

func (d *countedPermit) Acquire(context.Context) (func(), error) {
	d.acquired++
	return func() { d.released++ }, nil
}
func TestEvery429AttemptOwnsPermit(t *testing.T) {
	p := &countedPermit{}
	f := &gateFixture{limited: 2}
	g, e := NewGovernedModel(f, GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond, Shared: p})
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.Generate(context.Background(), nil)
	if e != nil || p.acquired != 3 || p.released != 3 || g.Snapshot().Attempts != 3 {
		t.Fatalf("error=%v permits=%+v stats=%+v", e, p, g.Snapshot())
	}
}

package llm

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sdk "github.com/meguminnnnnnnnn/go-openai"
	"sync"
	"testing"
	"time"
)

type gateFixture struct {
	model.ChatModel
	mu      sync.Mutex
	calls   int
	block   <-chan struct{}
	limited int
}

func (f *gateFixture) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if n <= f.limited {
		return nil, &sdk.APIError{HTTPStatusCode: 429}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &schema.Message{Role: schema.Assistant, Content: "{}", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: 100}}}, nil
}
func TestGovernorBoundedQueueAndCancellation(t *testing.T) {
	block := make(chan struct{})
	f := &gateFixture{block: block}
	g, e := NewGovernedModel(f, GovernorOptions{Concurrency: 1, QueueCapacity: 1, Attempts: 3, Backoff: time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 2)
	go func() { _, e := g.Generate(context.Background(), nil); done <- e }()
	waitFor := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatal("condition timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(func() bool { return g.Snapshot().Active == 1 })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, e := g.Generate(ctx, nil); done <- e }()
	waitFor(func() bool { return g.Snapshot().Queued == 1 })
	if _, e := g.Generate(context.Background(), nil); !errors.Is(e, ErrModelQueueFull) {
		t.Fatalf("unbounded queue: %v", e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(block)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	s := g.Snapshot()
	if s.Active != 0 || s.Queued != 0 || s.Rejected != 1 || s.Attempts != 1 || s.Tokens != 100 {
		t.Fatalf("leaked admission/metrics: %+v", s)
	}
}
func TestGovernorRetriesOnly429WithinBound(t *testing.T) {
	for _, limits := range []int{2, 5} {
		f := &gateFixture{limited: limits}
		g, _ := NewGovernedModel(f, GovernorOptions{Concurrency: 1, QueueCapacity: 0, Attempts: 3, Backoff: time.Millisecond})
		_, e := g.Generate(context.Background(), nil)
		if (limits == 2) != (e == nil) {
			t.Fatalf("limits=%d err=%v", limits, e)
		}
		if g.Snapshot().Attempts != 3 || g.Snapshot().Retries != 2 {
			t.Fatal(g.Snapshot())
		}
	}
}
func TestGovernorCancelsBackoffAndActiveRequest(t *testing.T) {
	for _, f := range []*gateFixture{{limited: 10}, {block: make(chan struct{})}} {
		g, _ := NewGovernedModel(f, GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, e := g.Generate(ctx, nil)
		if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > time.Second || g.Snapshot().Active != 0 {
			t.Fatalf("cancellation lost: %v %+v", e, g.Snapshot())
		}
	}
}
func TestGovernorDoesNotRetryPermanentError(t *testing.T) {
	f := &permanentFixture{}
	g, _ := NewGovernedModel(f, GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond})
	if _, e := g.Generate(context.Background(), nil); e == nil || g.Snapshot().Attempts != 1 {
		t.Fatal("permanent error retried")
	}
	if _, e := g.Stream(context.Background(), nil); e == nil {
		t.Fatal("stream bypassed governor")
	}
}

type permanentFixture struct{ model.ChatModel }

func (*permanentFixture) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, &sdk.APIError{HTTPStatusCode: 401}
}

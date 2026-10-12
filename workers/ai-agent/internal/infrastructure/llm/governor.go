package llm

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"sync/atomic"
	"time"
)

var ErrModelQueueFull = domain.ErrModelQueueFull

type GovernorOptions struct {
	Concurrency, QueueCapacity, Attempts int
	Backoff                              time.Duration
}
type GovernorStats struct{ Active, Queued, PeakActive, PeakQueued, Attempts, Retries, Rejected, Canceled, Completed, Tokens, QueueMillis int64 }
type GovernedModel struct {
	inner                                                                          model.ChatModel
	options                                                                        GovernorOptions
	active, waiting                                                                chan struct{}
	a, q, pa, pq, attempts, retries, rejected, canceled, completed, tokens, waitMS atomic.Int64
}

func NewGovernedModel(inner model.ChatModel, o GovernorOptions) (*GovernedModel, error) {
	if inner == nil || o.Concurrency < 1 || o.Concurrency > 128 || o.QueueCapacity < 0 || o.QueueCapacity > 4096 || o.Attempts < 1 || o.Attempts > 3 || o.Backoff <= 0 || o.Backoff > 5*time.Second {
		return nil, fmt.Errorf("invalid model governor policy")
	}
	return &GovernedModel{inner: inner, options: o, active: make(chan struct{}, o.Concurrency), waiting: make(chan struct{}, o.QueueCapacity)}, nil
}
func (g *GovernedModel) BindTools(t []*schema.ToolInfo) error { return g.inner.BindTools(t) }

// Streaming is intentionally unavailable until stream lifetime admission is implemented.
func (g *GovernedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("governed model supports Generate only")
}
func peak(p *atomic.Int64, n int64) {
	for old := p.Load(); n > old; old = p.Load() {
		if p.CompareAndSwap(old, n) {
			return
		}
	}
}
func (g *GovernedModel) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.active <- struct{}{}:
		return nil
	default:
	}
	select {
	case g.waiting <- struct{}{}:
	default:
		g.rejected.Add(1)
		return ErrModelQueueFull
	}
	n := g.q.Add(1)
	peak(&g.pq, n)
	start := time.Now()
	defer func() { <-g.waiting; g.q.Add(-1); g.waitMS.Add(time.Since(start).Milliseconds()) }()
	select {
	case g.active <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g *GovernedModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (reply *schema.Message, err error) {
	defer func() {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			g.canceled.Add(1)
		}
	}()
	if err = g.acquire(ctx); err != nil {
		return nil, err
	}
	n := g.a.Add(1)
	peak(&g.pa, n)
	defer func() { g.a.Add(-1); <-g.active }()
	for i := 0; i < g.options.Attempts; i++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		g.attempts.Add(1)
		reply, err = g.inner.Generate(ctx, input, opts...)
		if err == nil {
			g.completed.Add(1)
			if reply != nil && reply.ResponseMeta != nil && reply.ResponseMeta.Usage != nil {
				g.tokens.Add(int64(reply.ResponseMeta.Usage.TotalTokens))
			}
			return reply, nil
		}
		if apiStatus(err) != 429 || i+1 == g.options.Attempts {
			return nil, err
		}
		g.retries.Add(1)
		timer := time.NewTimer(g.options.Backoff * time.Duration(1<<i))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
	return nil, err
}
func (g *GovernedModel) Snapshot() GovernorStats {
	return GovernorStats{Active: g.a.Load(), Queued: g.q.Load(), PeakActive: g.pa.Load(), PeakQueued: g.pq.Load(), Attempts: g.attempts.Load(), Retries: g.retries.Load(), Rejected: g.rejected.Load(), Canceled: g.canceled.Load(), Completed: g.completed.Load(), Tokens: g.tokens.Load(), QueueMillis: g.waitMS.Load()}
}

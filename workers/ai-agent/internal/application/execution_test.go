package application

import (
	"context"
	"github.com/google/uuid"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"testing"
)

type busyRepo struct{ domain.DiagnosisRepository }

func (busyRepo) ExecuteOnce(context.Context, string, func(context.Context) (*domain.Diagnosis, error)) (*domain.Diagnosis, error) {
	return nil, &core.DeferredTaskError{Reason: "healthy owner"}
}

type progressFixture struct{ failures int }

func (p *progressFixture) PublishProgress(_ context.Context, _ string, e domain.StreamEvent) error {
	if e.EventType == "diagnosis_failed" {
		p.failures++
	}
	return nil
}
func TestHealthyOwnerDoesNotPublishFailure(t *testing.T) {
	p := &progressFixture{}
	s := NewServiceWithRunner(busyRepo{}, nil, p, 1)
	if err := s.DiagnoseBatch(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("deferral expected")
	}
	if p.failures != 0 {
		t.Fatalf("healthy owner emitted %d failure events", p.failures)
	}
}

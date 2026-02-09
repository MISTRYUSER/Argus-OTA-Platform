package application

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

func TestRunGuards_BatchIDInvalid(t *testing.T) {
	res := RunGuards(context.Background(), &domain.DiagnosisContext{
		BatchID: "not-a-uuid",
	})
	if res.Passed {
		t.Fatalf("expected guard failure for invalid batch id")
	}
	if res.Blocked == nil || res.Blocked.RuleName != "batch_id_uuid_format" {
		t.Fatalf("unexpected blocked info: %+v", res.Blocked)
	}
}

func TestRunGuards_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := RunGuards(ctx, &domain.DiagnosisContext{
		BatchID: uuid.NewString(),
	})
	if res.Passed {
		t.Fatalf("expected guard failure for canceled context")
	}
	if res.Blocked == nil || res.Blocked.RuleName != "context_active" {
		t.Fatalf("unexpected blocked info: %+v", res.Blocked)
	}
}

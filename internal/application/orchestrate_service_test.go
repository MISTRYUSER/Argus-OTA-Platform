package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"testing"
)

type eventSink struct{ events []domain.DomainEvent }

func (s *eventSink) PublishEvents(_ context.Context, e []domain.DomainEvent) error {
	s.events = append(s.events, e...)
	return nil
}
func (s *eventSink) Close() error { return nil }
func TestGatheringUsesLegalTransitions(t *testing.T) {
	b := &domain.Batch{ID: uuid.New(), Status: domain.BatchStatusGathering, TotalFiles: 2, ProcessedFiles: 2}
	svc := NewOrchestrateService(&mockBatchRepo{findByIDBatch: b}, nil, &eventSink{})
	msg, _ := json.Marshal(map[string]string{"event_type": "GatheringCompleted", "batch_id": b.ID.String()})
	if err := svc.HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if b.Status != domain.BatchStatusDiagnosing {
		t.Fatalf("status=%s", b.Status)
	}
}
func TestBatchCreatedReplayDoesNotRegress(t *testing.T) {
	for _, status := range []domain.BatchStatus{domain.BatchStatusScattering, domain.BatchStatusGathering, domain.BatchStatusCompleted} {
		b := &domain.Batch{ID: uuid.New(), Status: status}
		sink := &eventSink{}
		svc := NewOrchestrateService(&mockBatchRepo{findByIDBatch: b}, nil, sink)
		msg, _ := json.Marshal(map[string]string{"event_type": "BatchCreated", "batch_id": b.ID.String()})
		if err := svc.HandleMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		if b.Status != status {
			t.Fatalf("regressed %s to %s", status, b.Status)
		}
	}
}

func TestMalformedCompletionIdentifiersArePermanent(t *testing.T) {
	for _, typ := range []string{"FileParsed", "DiagnosisCompleted"} {
		b := &domain.Batch{ID: uuid.New(), Status: domain.BatchStatusDiagnosing}
		svc := NewOrchestrateService(&mockBatchRepo{findByIDBatch: b}, nil, &eventSink{})
		msg, _ := json.Marshal(map[string]string{"event_type": typ, "batch_id": b.ID.String(), "file_id": "bad", "diagnosis_id": "bad"})
		err := svc.HandleMessage(context.Background(), msg)
		var malformed *domain.InvalidEventError
		if !errors.As(err, &malformed) {
			t.Fatalf("type=%s err=%v", typ, err)
		}
	}
}

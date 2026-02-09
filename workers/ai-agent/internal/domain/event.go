package domain

import (
	"time"

	"github.com/google/uuid"
)

type DomainEvent interface {
	EventType() string
	AggregateID() string
	OccurredAt() time.Time
}

// DiagnosisStatusChangedEvent 诊断状态变更事件
type DiagnosisStatusChangedEvent struct {
	aggregateID  uuid.UUID
	BatchID      uuid.UUID
	OldStatus    DiagnosisStatus
	NewStatus    DiagnosisStatus
	occurredAt   time.Time
}

func NewDiagnosisStatusChangedEvent(aggregateID, batchID uuid.UUID, oldStatus, newStatus DiagnosisStatus) *DiagnosisStatusChangedEvent {
	return &DiagnosisStatusChangedEvent{
		aggregateID: aggregateID,
		BatchID:     batchID,
		OldStatus:   oldStatus,
		NewStatus:   newStatus,
		occurredAt:  time.Now(),
	}
}

func (e *DiagnosisStatusChangedEvent) EventType() string {
	return "DiagnosisStatusChanged"
}

func (e *DiagnosisStatusChangedEvent) AggregateID() string {
	return e.aggregateID.String()
}

func (e *DiagnosisStatusChangedEvent) OccurredAt() time.Time {
	return e.occurredAt
}

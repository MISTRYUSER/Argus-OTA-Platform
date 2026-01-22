package domain
import (
	"github.com/google/uuid"
	"time"

)

type DomainEvent interface {
	OccurredOn() time.Time
	AggregateID() uuid.UUID
	EventType() string
}

type BatchCreated struct {
	BatchID     uuid.UUID
	VehicleID   string
	VIN         string
	OccurredAt  time.Time
}

type BatchStatusChanged struct {
	BatchID     uuid.UUID
	OldStatus   BatchStatus
	NewStatus   BatchStatus
	OccurredAt  time.Time
}

// FileParsed - 文件解析完成事件（C++ Worker 发布）
type FileParsed struct {
	BatchID     uuid.UUID
	FileID      uuid.UUID
	OccurredAt  time.Time
}

// FileParsed implements DomainEvent interface
func (e FileParsed) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e FileParsed) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e FileParsed) EventType() string {
	return "FileParsed"
}

// ============================================================================
// DomainEvent Interface Implementation
// ============================================================================

// BatchCreated implements DomainEvent interface
func (e BatchCreated) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e BatchCreated) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e BatchCreated) EventType() string {
	return "BatchCreated"
}

// StatusChanged implements DomainEvent interface
func (e BatchStatusChanged) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e BatchStatusChanged) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e BatchStatusChanged) EventType() string {
	return "StatusChanged"
}

package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)
type Batch struct {
	ID                  uuid.UUID
	VehicleID           string
	VIN                 string
	Status              BatchStatus
	UploadTime          time.Time
	TotalFiles          int
	ProcessedFiles      int
	ExpectedWorkerCount int
	CompletedWorkerCount int
	MinIOBucket         string
	MiniIOPrefix        string
	ErrorMessage        string
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	eventlog            []DomainEvent
}
func NewBatch(vehicleID, vin string, expectedWorkers int) (*Batch, error) {
	if vehicleID == "" {
		return nil,errors.New("vehicleId is empty")
	}
	if vin == "" {
		return nil,errors.New("vin is empty")
	}
	if expectedWorkers <= 0 {
		return nil,errors.New("exceptedWorkers is <= 0")
	}
	id  := uuid.New()
	now := time.Now()

	event := BatchCreated {
		BatchID: 	id,
		VehicleID: 	vehicleID,
		VIN: 		vin,
		OccurredAt: now,
	}
	return &Batch{
		ID:                  id,
		VehicleID:           vehicleID,
		VIN:                 vin,
		Status:              BatchStatusPending,
		UploadTime:          now,
		TotalFiles:          0,
		ProcessedFiles:      0,
		ExpectedWorkerCount: expectedWorkers,
		CompletedWorkerCount: 0,
		MinIOBucket:         "",
		MiniIOPrefix:        "",
		ErrorMessage:        "",
		CompletedAt:         nil,
		CreatedAt:           now,
		UpdatedAt:           now,
		eventlog:            []DomainEvent{event},
	}, nil


}
func (b *Batch) TransitionTo(status BatchStatus) error {
	if !b.Status.CanTransitionTo(status) {
		return errors.New("invalid status transition from " + b.Status.String() + " to " + status.String())
	}
	b.Status = status
	b.UpdatedAt = time.Now()
	return nil
}
func (b *Batch) IncrementWorkerCount() error {
	if b.CompletedWorkerCount >= b.ExpectedWorkerCount {
		return errors.New("all workers are already completed")
	}
	b.CompletedWorkerCount++
	b.UpdatedAt = time.Now()
	return nil
}
func (b *Batch) AddFile(fileID uuid.UUID) error {
	//check 
	if fileID == uuid.Nil {
		return errors.New("fileID is nil")
	}
	//just special status can add file
	if b.Status != BatchStatusPending && b.Status != BatchStatusUploaded {
		return errors.New("batch is not in pending or uploaded status" + b.Status.String())
	}

	b.TotalFiles++
	b.UpdatedAt = time.Now()
	return nil
}

func (b *Batch) MakeFileProcessed() error {
	 	if b.ProcessedFiles >= b.TotalFiles {
			return errors.New("all files are already processed")
		}
		b.ProcessedFiles++
		b.UpdatedAt = time.Now()

		return nil
}
func (b *Batch) GetEvents() []DomainEvent {
	if len(b.eventlog) == 0 {
		return []DomainEvent{}
	}
	events := make([]DomainEvent,len(b.eventlog))
	copy(events,b.eventlog)
	return events
}
func (b *Batch) ClearEvents() {
	b.eventlog = []DomainEvent{}
}


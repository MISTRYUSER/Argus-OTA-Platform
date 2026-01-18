package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

type BatchService struct {
	batchRepo domain.BatchRepository
	kafka 	  messaging.KafkaEventPublisher
}

func NewBatchService(batchRepo domain.BatchRepository,kafka messaging.KafkaEventPublisher) *BatchService {
	return &BatchService{
		batchRepo: batchRepo,
		kafka:     kafka,
	}
}

func (s *BatchService) CreateBatch (
	ctx context.Context,
	vehicleID, vin string,
	expectedWorkers int,
) (*domain.Batch,error) {
	batch, err := domain.NewBatch(vehicleID,vin,expectedWorkers)
	if err != nil {
		return nil,err
	}
	if err := s.batchRepo.Save(ctx,batch); err != nil {
		return nil,err
	}
	events := batch.GetEvents()

	if err := s.kafka.PublishEvents(ctx,events);err != nil {
		fmt.Printf("Failed to publish events: %v\n", err)
	}
	batch.ClearEvents()

	return batch,s.batchRepo.Save(ctx,batch)
}

func (s *BatchService)TransitionBatchStatus(
	ctx context.Context,
	batchID uuid.UUID,
	newStatus domain.BatchStatus,
) error {
	batch,err := s.batchRepo.FindByID(ctx,batchID) 
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found %s",batchID)
	}

	if err := batch.TransitionTo(newStatus); err != nil {
		return err
	}
	if err := s.batchRepo.Save(ctx,batch); err != nil {
		return err
	}

	events := batch.GetEvents()
	if len(events) > 0 {
		if err := s.kafka.PublishEvents(ctx,events);err != nil {
			fmt.Printf("Failed to publish events: %v\n", err)
		}
	}
	batch.ClearEvents()

	return s.batchRepo.Save(ctx,batch)

}

func (s *BatchService) AddFile (
	ctx context.Context,
	batchID uuid.UUID,
	fileID uuid.UUID,
) error {
	batch, err := s.batchRepo.FindByID(ctx,batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found %s",batchID)
	}
	if err := batch.AddFile(fileID); err != nil {
		return err
	}
	return s.batchRepo.Save(ctx,batch)
}
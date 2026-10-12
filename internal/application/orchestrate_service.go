package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	"time"
)

type OrchestrateService struct {
	batchRepo domain.BatchRepository
	redis     *redis.RedisClient
	kafka     messaging.KafkaEventPublisher
}

func NewOrchestrateService(repo domain.BatchRepository, r *redis.RedisClient, k messaging.KafkaEventPublisher) *OrchestrateService {
	return &OrchestrateService{repo, r, k}
}
func updateBatch(ctx context.Context, repo domain.BatchRepository, publisher messaging.KafkaEventPublisher, id uuid.UUID, mutate func(*domain.Batch) error) error {
	if atomic, ok := repo.(domain.TransactionalBatchRepository); ok {
		return atomic.UpdateBatch(ctx, id, mutate)
	}
	b, err := repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("batch not found: %s", id)
	}
	if err = mutate(b); err != nil {
		return err
	}
	if err = repo.Save(ctx, b); err != nil {
		return err
	}
	if events := b.GetEvents(); len(events) > 0 {
		if publisher == nil {
			return fmt.Errorf("publisher is nil")
		}
		if err = publisher.PublishEvents(ctx, events); err != nil {
			return err
		}
	}
	b.ClearEvents()
	return nil
}
func (s *OrchestrateService) HandleMessage(ctx context.Context, data []byte) error {
	var e struct {
		Type        string `json:"event_type"`
		BatchID     string `json:"batch_id"`
		FileID      string `json:"file_id"`
		DiagnosisID string `json:"diagnosis_id"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return &domain.InvalidEventError{Err: err}
	}
	if e.Type == "" {
		return &domain.InvalidEventError{Err: fmt.Errorf("missing event_type")}
	}
	if e.Type != "BatchCreated" && e.Type != "FileParsed" && e.Type != "GatheringCompleted" && e.Type != "DiagnosisCompleted" && e.Type != "DiagnosisFailed" {
		return nil
	}
	id, err := uuid.Parse(e.BatchID)
	if err != nil {
		return &domain.InvalidEventError{Err: fmt.Errorf("invalid batch_id: %w", err)}
	}
	if e.Type == "FileParsed" {
		fileID, err := uuid.Parse(e.FileID)
		if err != nil {
			return &domain.InvalidEventError{Err: err}
		}
		mutate := func(b *domain.Batch) error {
			if b.Status == domain.BatchStatusUploaded {
				if err := b.TransitionTo(domain.BatchStatusScattering); err != nil {
					return err
				}
			}
			if b.TotalFiles > 0 && b.ProcessedFiles >= b.TotalFiles && b.Status == domain.BatchStatusScattering {
				if err := b.TransitionTo(domain.BatchStatusScattered); err != nil {
					return err
				}
				if err := b.TransitionTo(domain.BatchStatusGathering); err != nil {
					return err
				}
				b.AddEvent(domain.WireEvent{Type: "AllFilesScattered", BatchID: id, Timestamp: time.Now()})
			}
			return nil
		}
		atomic, ok := s.batchRepo.(domain.TransactionalBatchRepository)
		if !ok {
			return fmt.Errorf("durable file completion repository required")
		}
		if err = atomic.RecordFileCompletion(ctx, id, fileID, mutate); err != nil {
			return err
		}
		// Redis is a best-effort progress projection; durable file IDs remain authoritative.
		if s.redis != nil {
			_ = s.redis.SADDWithTTL(ctx, "batch:"+id.String()+":processed_files", 24*time.Hour, fileID.String())
		}
		return nil
	}
	mutate := func(b *domain.Batch) error {
		if b.Status == domain.BatchStatusCompleted {
			return nil
		}
		if b.Status != domain.BatchStatusDiagnosing {
			return &domain.InvalidEventError{Err: fmt.Errorf("diagnosis arrived before diagnosing")}
		}
		if err := b.TransitionTo(domain.BatchStatusCompleted); err != nil {
			return err
		}
		now := time.Now()
		b.CompletedAt = &now
		return nil
	}
	if e.Type == "DiagnosisCompleted" {
		diagnosisID, err := uuid.Parse(e.DiagnosisID)
		if err != nil {
			return &domain.InvalidEventError{Err: fmt.Errorf("invalid diagnosis_id: %w", err)}
		}
		verifier, ok := s.batchRepo.(interface {
			CompleteDiagnosis(context.Context, uuid.UUID, uuid.UUID, func(*domain.Batch) error) error
		})
		if !ok {
			return fmt.Errorf("durable completion verifier required")
		}
		return verifier.CompleteDiagnosis(ctx, id, diagnosisID, mutate)
	}
	if e.Type == "DiagnosisFailed" {
		return s.FailBatch(ctx, id, fmt.Errorf("investigation failed; inspect durable diagnosis and DLQ"))
	}
	return updateBatch(ctx, s.batchRepo, s.kafka, id, func(b *domain.Batch) error {
		switch e.Type {
		case "BatchCreated":
			if b.Status == domain.BatchStatusUploaded {
				return b.TransitionTo(domain.BatchStatusScattering)
			}
			if b.Status == domain.BatchStatusPending {
				return fmt.Errorf("upload not completed")
			}
		case "GatheringCompleted":
			if b.Status == domain.BatchStatusDiagnosing || b.Status == domain.BatchStatusCompleted {
				return nil
			}
			if b.Status != domain.BatchStatusGathering {
				return fmt.Errorf("aggregation arrived before gathering: %s", b.Status)
			}
			if err := b.TransitionTo(domain.BatchStatusGathered); err != nil {
				return err
			}
			if err := b.TransitionTo(domain.BatchStatusDiagnosing); err != nil {
				return err
			}
			b.AddEvent(domain.WireEvent{Type: "DiagnosisRequested", BatchID: id, Timestamp: time.Now()})

		}
		return nil
	})
}
func (s *OrchestrateService) HandleStuckBatch(ctx context.Context, b *domain.Batch) error {
	return updateBatch(ctx, s.batchRepo, s.kafka, b.ID, func(current *domain.Batch) error {
		switch current.Status {
		case domain.BatchStatusScattering:
			if current.TotalFiles > 0 && current.ProcessedFiles >= current.TotalFiles {
				if err := current.TransitionTo(domain.BatchStatusScattered); err != nil {
					return err
				}
				if err := current.TransitionTo(domain.BatchStatusGathering); err != nil {
					return err
				}
				current.AddEvent(domain.WireEvent{Type: "AllFilesScattered", BatchID: current.ID, Timestamp: time.Now(), Data: map[string]interface{}{"retry": true}})
			}
		case domain.BatchStatusGathering:
			current.AddEvent(domain.WireEvent{Type: "AllFilesScattered", BatchID: current.ID, Timestamp: time.Now(), Data: map[string]interface{}{"retry": true}})
		case domain.BatchStatusDiagnosing:
			current.AddEvent(domain.WireEvent{Type: "DiagnosisRequested", BatchID: current.ID, Timestamp: time.Now(), Data: map[string]interface{}{"retry": true}})
		}
		return nil
	})
}

func (s *OrchestrateService) FailBatch(ctx context.Context, id uuid.UUID, cause error) error {
	return updateBatch(ctx, s.batchRepo, s.kafka, id, func(b *domain.Batch) error {
		if b.Status == domain.BatchStatusCompleted || b.Status == domain.BatchStatusFailed {
			return nil
		}
		b.ErrorMessage = domain.SafeText(cause.Error(), 1500)
		return b.TransitionTo(domain.BatchStatusFailed)
	})
}
func (s *OrchestrateService) HandleFailure(ctx context.Context, body []byte, cause error) error {
	var malformed *domain.InvalidEventError
	if errors.As(cause, &malformed) {
		return nil
	}
	var e struct {
		ID string `json:"batch_id"`
	}
	if json.Unmarshal(body, &e) != nil {
		return nil
	}
	id, err := uuid.Parse(e.ID)
	if err != nil {
		return nil
	}
	b, err := s.batchRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	return s.FailBatch(ctx, id, cause)
}

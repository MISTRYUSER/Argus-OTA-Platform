package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

type OrchestrateService struct {
	batchRepo domain.BatchRepository
	redis     *redis.RedisClient
	kafka     messaging.KafkaEventPublisher
}

func NewOrchestrateService(
	batchRepo domain.BatchRepository,
	redis *redis.RedisClient,
	kafka messaging.KafkaEventPublisher,
) *OrchestrateService {
	return &OrchestrateService{
		batchRepo: batchRepo,
		redis:     redis,
		kafka:     kafka,
	}
}

func (s *OrchestrateService) HandleMessage(ctx context.Context, data []byte) error {
	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	eventType := event["event_type"].(string)

	switch eventType {
	case "BatchCreated":
		return s.handleBatchCreated(ctx, event)

	case "FileParsed":
		return s.handleFileParsed(ctx, event)

	case "StatusChanged":
		return s.handleStatusChanged(ctx, event)

	default:
		log.Printf("Unknown event type: %s", eventType)
	}

	return nil
}

func (s *OrchestrateService) handleBatchCreated(ctx context.Context, event map[string]interface{}) error {
	batchIDStr := event["batch_id"].(string)
	batchID, _ := uuid.Parse(batchIDStr)

	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}

	// 3. 状态转换：pending → uploaded
	if err := batch.TransitionTo(domain.BatchStatusUploaded); err != nil {
		return err
	}

	// 4. 状态转换：uploaded → scattering
	if err := batch.TransitionTo(domain.BatchStatusScattering); err != nil {
		return err
	}

	// 5. 保存状态
	if err := s.batchRepo.Save(ctx, batch); err != nil {
		return err
	}

	events := batch.GetEvents()
	if err := s.kafka.PublishEvents(ctx, events); err != nil {
		log.Printf("Failed to publish events: %v", err)
	}
	batch.ClearEvents()
	log.Printf("[Orchestrator] Batch %s transitioned to scattering", batchID)
	return nil
}

func (s *OrchestrateService) handleFileParsed(ctx context.Context, event map[string]interface{}) error {
	batchIDStr := event["batch_id"].(string)
	batchID, _ := uuid.Parse(batchIDStr)
	fileIDStr := event["file_id"].(string)

	// use set handle file
	key := fmt.Sprintf("batch:%s:processed_files", batchID)
	added, err := s.redis.SADD(ctx, key, fileIDStr)
	if err != nil {
		return err
	}
	if added > 0 {
		s.redis.EXPIRE(ctx, key, 24*time.Hour)
	}
	count, err := s.redis.SCARD(ctx, key)
	if err != nil {
		return err
	}
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}
	log.Printf("[Orchestrator] Progress: %d/%d files processed", count, batch.TotalFiles)
	if count == int64(batch.TotalFiles) {
		log.Printf("[Orchestrator] All files processed, transitioning to gathered")
		if err := batch.TransitionTo(domain.BatchStatusGathered); err != nil {
			return err
		}
		if err := s.batchRepo.Save(ctx, batch); err != nil {
			return err
		}
		events := batch.GetEvents()
		if err := s.kafka.PublishEvents(ctx, events); err != nil {
			log.Printf("Failed to publish events: %v", err)
		}
		batch.ClearEvents()
		s.redis.DEL(ctx, key)
	} else {
		log.Printf("[Orchestrator] Waiting for more files (%d/%d)", count, batch.TotalFiles)
	}
	return nil
}

func (s *OrchestrateService) handleStatusChanged(ctx context.Context, event map[string]interface{}) error {
	// StatusChanged 事件处理（日志记录即可，不触发额外逻辑）
	batchIDStr, ok := event["batch_id"].(string)
	if !ok {
		return fmt.Errorf("invalid batch_id in event")
	}

	oldStatus, _ := event["old_status"].(string)
	newStatus, _ := event["new_status"].(string)

	log.Printf("[Orchestrator] StatusChanged: Batch %s, %s -> %s", batchIDStr, oldStatus, newStatus)
	return nil
}
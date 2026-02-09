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

	case "GatheringCompleted":
		return s.handleGatheringCompleted(ctx, event)

	case "DiagnosisCompleted":
		return s.handleDiagnosisCompleted(ctx, event)

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
	if batch == nil {  // ← 添加这个检查
		return fmt.Errorf("batch not found: %s", batchID)
	}
  
  
	switch batch.Status {
	case domain.BatchStatusPending:
		batch.TransitionTo(domain.BatchStatusUploaded)
		batch.TransitionTo(domain.BatchStatusScattering)
	case domain.BatchStatusUploaded:
		batch.TransitionTo(domain.BatchStatusScattering)
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

	// Redis Barrier 计数（使用 Set，天然幂等）
	key := fmt.Sprintf("batch:%s:processed_files", batchID)
	added, err := s.redis.SADD(ctx, key, fileIDStr)
	if err != nil {
		return fmt.Errorf("failed to add to Redis set: %w", err)
	}

	// 设置过期时间（只在新添加时设置，避免重复操作）
	if added > 0 {
		s.redis.EXPIRE(ctx, key, 24*time.Hour)
	}

	// 获取已处理文件数
	count, err := s.redis.SCARD(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get Redis set size: %w", err)
	}

	// 查询 Batch 信息
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found: %s", batchID)
	}

	// 更新处理进度（仅内存，不持久化）
	batch.ProcessedFiles = int(count)
	log.Printf("[Orchestrator] Progress: %d/%d files processed", count, batch.TotalFiles)

	// 检查是否所有文件都已处理
	if count == int64(batch.TotalFiles) {
		log.Printf("[Orchestrator] All files processed for batch %s, waiting for GatheringCompleted event", batchID)
		// ✅ 修改：不再立即转换状态，等待 GatheringCompleted 事件
		// 清理 Redis Barrier（避免内存泄漏）
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

// handleGatheringCompleted - 处理 Python Worker 完成数据聚合事件
func (s *OrchestrateService) handleGatheringCompleted(ctx context.Context, event map[string]interface{}) error {
	batchIDStr := event["batch_id"].(string)
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		return fmt.Errorf("invalid batch_id: %w", err)
	}

	// 查询 Batch
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found: %s", batchID)
	}

	log.Printf("[Orchestrator] GatheringCompleted received for batch %s, current status: %s",
		batchID, batch.Status)

	// 状态转换：scattering → gathered → diagnosing
	// 注意：可能状态已经是 scattered（Mock Worker 模拟），需要检查当前状态
	switch batch.Status {
	case domain.BatchStatusScattering:
		// scattering → gathered
		if err := batch.TransitionTo(domain.BatchStatusGathered); err != nil {
			return fmt.Errorf("failed to transition to gathered: %w", err)
		}
		log.Printf("[Orchestrator] Status: scattering → gathered")

		// gathered → diagnosing
		if err := batch.TransitionTo(domain.BatchStatusDiagnosing); err != nil {
			return fmt.Errorf("failed to transition to diagnosing: %w", err)
		}
		log.Printf("[Orchestrator] Status: gathered → diagnosing")

	case domain.BatchStatusScattered:
		// scattered → diagnosing（兼容 Mock Worker 的状态）
		if err := batch.TransitionTo(domain.BatchStatusDiagnosing); err != nil {
			return fmt.Errorf("failed to transition to diagnosing: %w", err)
		}
		log.Printf("[Orchestrator] Status: scattered → diagnosing")

	default:
		return fmt.Errorf("unexpected batch status: %s, expected scattering or scattered", batch.Status)
	}

	// 保存到数据库
	if err := s.batchRepo.Save(ctx, batch); err != nil {
		return fmt.Errorf("failed to save batch: %w", err)
	}

	// 发布状态变更事件
	events := batch.GetEvents()
	if len(events) > 0 {
		if err := s.kafka.PublishEvents(ctx, events); err != nil {
			log.Printf("Failed to publish events: %v", err)
		}
		batch.ClearEvents()
	}

	log.Printf("[Orchestrator] Batch %s is now in diagnosing status", batchID)
	return nil
}

// handleDiagnosisCompleted - 处理 AI Agent 完成诊断事件
func (s *OrchestrateService) handleDiagnosisCompleted(ctx context.Context, event map[string]interface{}) error {
	batchIDStr := event["batch_id"].(string)
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		return fmt.Errorf("invalid batch_id: %w", err)
	}

	diagnosisIDStr := event["diagnosis_id"].(string)
	diagnosisID, err := uuid.Parse(diagnosisIDStr)
	if err != nil {
		return fmt.Errorf("invalid diagnosis_id: %w", err)
	}

	// 查询 Batch
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found: %s", batchID)
	}

	log.Printf("[Orchestrator] DiagnosisCompleted received for batch %s, diagnosis %s",
		batchID, diagnosisID)

	// 状态转换：diagnosing → completed
	if batch.Status != domain.BatchStatusDiagnosing {
		return fmt.Errorf("unexpected batch status: %s, expected diagnosing", batch.Status)
	}

	if err := batch.TransitionTo(domain.BatchStatusCompleted); err != nil {
		return fmt.Errorf("failed to transition to completed: %w", err)
	}
	log.Printf("[Orchestrator] Status: diagnosing → completed")

	// 设置完成时间
	now := time.Now()
	batch.CompletedAt = &now

	// 保存到数据库
	if err := s.batchRepo.Save(ctx, batch); err != nil {
		return fmt.Errorf("failed to save batch: %w", err)
	}

	// 发布状态变更事件
	events := batch.GetEvents()
	if len(events) > 0 {
		if err := s.kafka.PublishEvents(ctx, events); err != nil {
			log.Printf("Failed to publish events: %v", err)
		}
		batch.ClearEvents()
	}

	log.Printf("[Orchestrator] ✅ Batch %s processing completed! Final status: %s",
		batchID, batch.Status)
	return nil
}

// HandleStuckBatch 处理卡住的批次（补偿任务）
func (s *OrchestrateService) HandleStuckBatch(ctx context.Context, batch *domain.Batch) error {
	log.Printf("[Compensation] Handling stuck batch: id=%s, status=%s, updated_at=%s",
		batch.ID, batch.Status, batch.UpdatedAt)

	switch batch.Status {
	case domain.BatchStatusScattering:
		// ✅ 所有文件已处理，但未收到 GatheringCompleted 事件
		// 检查是否所有文件都已处理
		if batch.ProcessedFiles >= batch.TotalFiles {
			// 模拟发布 GatheringCompleted 事件（重新触发 Python Worker）
			log.Printf("[Compensation] Re-triggering GatheringCompleted for batch %s", batch.ID)

			// 构造事件数据
			event := map[string]interface{}{
				"event_type":  "GatheringCompleted",
				"version":     "1.0",
				"batch_id":    batch.ID.String(),
				"total_files": batch.TotalFiles,
				"chart_files": []string{}, // 空列表，让 Python Worker 重新聚合
				"timestamp":   time.Now().Format(time.RFC3339),
			}

			// 直接调用 handleGatheringCompleted
			if err := s.handleGatheringCompleted(ctx, event); err != nil {
				log.Printf("[Compensation] Failed to handle GatheringCompleted: %v", err)
				return fmt.Errorf("failed to handle gathering completed: %w", err)
			}

			log.Printf("[Compensation] Successfully re-triggered GatheringCompleted for batch %s", batch.ID)
		} else {
			log.Printf("[Compensation] Batch %s still waiting for files (%d/%d), skipping compensation",
				batch.ID, batch.ProcessedFiles, batch.TotalFiles)
		}

	case domain.BatchStatusDiagnosing:
		// ✅ 诊断超时，标记为失败
		log.Printf("[Compensation] Diagnosis timeout for batch %s, marking as failed", batch.ID)

		if err := batch.TransitionTo(domain.BatchStatusFailed); err != nil {
			return fmt.Errorf("failed to transition to failed: %w", err)
		}

		batch.ErrorMessage = "Diagnosis timeout after 10 minutes"

		// 保存到数据库
		if err := s.batchRepo.Save(ctx, batch); err != nil {
			return fmt.Errorf("failed to save batch: %w", err)
		}

		// 发布状态变更事件
		events := batch.GetEvents()
		if len(events) > 0 {
			if err := s.kafka.PublishEvents(ctx, events); err != nil {
				log.Printf("Failed to publish events: %v", err)
			}
			batch.ClearEvents()
		}

		log.Printf("[Compensation] Batch %s marked as failed", batch.ID)

	default:
		log.Printf("[Compensation] Unexpected batch status %s for batch %s, skipping", batch.Status, batch.ID)
	}

	return nil
}
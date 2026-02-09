package application

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

type BatchService struct {
	batchRepo domain.BatchRepository
	fileRepo  domain.FileRepository
	kafka     messaging.KafkaEventPublisher
}

func NewBatchService(
	batchRepo domain.BatchRepository,
	fileRepo domain.FileRepository,
	kafka messaging.KafkaEventPublisher,
) *BatchService {
	return &BatchService{
		batchRepo: batchRepo,
		fileRepo:  fileRepo,
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

	// 两阶段上传设计：创建 Batch 时不发布 Kafka 事件
	// BatchCreated 事件将在所有文件上传完成后（CompleteUpload）发布

	return batch,nil
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

	// P1 修复：先发布 Kafka 事件，确保事件发布成功后再持久化状态
	// 这样可以避免"状态已前进但下游未感知"的分叉问题
	events := batch.GetEvents()
	if len(events) > 0 {
		if err := s.kafka.PublishEvents(ctx, events); err != nil {
			// 发布失败，状态不应持久化
			return fmt.Errorf("failed to publish events: %w", err)
		}
	}

	// 事件发布成功后，清除事件日志并保存状态
	batch.ClearEvents()
	return s.batchRepo.Save(ctx, batch)

}

func (s *BatchService) AddFile(
	ctx context.Context,
	batchID uuid.UUID,
	fileID uuid.UUID,
	originalFilename string,
	fileSize int64,
	minioPath string,
) error {
	// 1. 验证 Batch 存在
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return fmt.Errorf("batch not found %s", batchID)
	}

	// 2. 先校验状态并增加 Batch 的 TotalFiles 计数（P1 修复：先校验，避免脏数据）
	if err := batch.AddFile(fileID); err != nil {
		return err
	}

	// 3. 创建 File 记录
	now := time.Now()
	file := &domain.File{
		ID:               fileID,
		BatchID:          batchID,
		Filename:         fileID.String(), // 使用 fileID 作为 filename
		OriginalFilename: originalFilename,
		FileSize:         fileSize,
		FileType:         "", // 可选：从文件扩展名推断
		UploadTime:       now,
		MinIOPath:        minioPath,
		MinIOETag:        "", // MinIO SDK 返回的 ETag
		ProcessingStatus: domain.FileStatusPending,
		ParseDurationMs:  0,
		RecordCount:      0,
		ErrorMessage:     "",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 4. 保存 File 到数据库（状态已校验，不会产生脏数据）
	if err := s.fileRepo.Save(ctx, file); err != nil {
		// 回滚：减少 TotalFiles 计数
		batch.TotalFiles--
		batch.UpdatedAt = now
		return fmt.Errorf("failed to save file: %w", err)
	}

	// 5. 保存 Batch
	return s.batchRepo.Save(ctx, batch)
}
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
)

type QueryService struct {
	batchRepo  domain.BatchRepository
	reportRepo domain.ReportRepository
	cache      *redis.RedisClient
	sf         singleflight.Group
}

func NewQueryService(
	batchRepo domain.BatchRepository,
	reportRepo domain.ReportRepository,
	cache *redis.RedisClient,
) *QueryService {
	return &QueryService{
		batchRepo:  batchRepo,
		reportRepo: reportRepo,
		cache:      cache,
	}
}

// GetReport 获取报告（使用 Singleflight 防缓存击穿）
//
// 面试考点：
// Q: Singleflight 如何防止缓存击穿？
// A: 100 个并发请求查询同一个 batchID，sf.Do() 会将它们合并为 1 次执行
func (s *QueryService) GetReport(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	key := batchID.String()

	resultCh := s.sf.DoChan(key, func() (interface{}, error) {
		// 共享查询不应被首个请求的取消信号中断，避免拖累同 key 等待者
		sharedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		log.Printf("[QueryService] Singleflight executing, key=%s", key)

		// 1. 先查缓存
		report, err := s.getReportFromCache(sharedCtx, batchID)
		if err == nil && report != nil {
			log.Printf("[QueryService] Cache HIT: batchID=%s", batchID)
			return report, nil
		}

		log.Printf("[QueryService] Cache MISS: batchID=%s, querying database...", batchID)

		// 2. 缓存未命中，查数据库
		report, err = s.getReportFromDatabase(sharedCtx, batchID)
		if err != nil {
			return nil, fmt.Errorf("failed to get report from database: %w", err)
		}

		// 3. 写入缓存
		if err := s.setReportToCache(sharedCtx, report, 10*time.Minute); err != nil {
			log.Printf("[QueryService] Warning: failed to set cache: %v", err)
		}

		return report, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		if result.Shared {
			log.Printf("[QueryService] Request was shared (merged with other concurrent requests)")
		}

		report, ok := result.Val.(*domain.Report)
		if !ok || report == nil {
			return nil, fmt.Errorf("unexpected report type: %T", result.Val)
		}
		return report, nil
	}
}

// getReportFromCache 从缓存获取报告（私有方法）
func (s *QueryService) getReportFromCache(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	if s.cache == nil {
		return nil, nil
	}

	key := fmt.Sprintf("report:%s", batchID)
	data, err := s.cache.GET(ctx, key)
	if err != nil {
		return nil, err // Redis 错误
	}

	if data == "" {
		return nil, nil // 缓存未命中（不是错误）
	}

	// 反序列化 JSON → Report
	var report domain.Report
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal report: %w", err)
	}

	return &report, nil
}

// setReportToCache 设置缓存（私有方法）
func (s *QueryService) setReportToCache(ctx context.Context, report *domain.Report, ttl time.Duration) error {
	if s.cache == nil {
		return nil
	}

	key := fmt.Sprintf("report:%s", report.BatchID)

	// 1. 序列化 Report → JSON
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	// 2. 写入 Redis（带过期时间）
	return s.cache.SET(ctx, key, string(data), ttl)
}
func (s *QueryService) getReportFromDatabase(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	report, err := s.reportRepo.FindByBatchID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to query report repository: %w", err)
	}
	if report != nil {
		return report, nil
	}

	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch: %w", err)
	}
	if batch == nil {
		return nil, fmt.Errorf("batch not found: %s", batchID)
	}

	report = domain.NewReport(batch)
	if err := s.reportRepo.Save(ctx, report); err != nil {
		log.Printf("[QueryService] Warning: failed to save report: %v", err)
	}
	return report, nil
}
func (s *QueryService) GetProgress(ctx context.Context, batchID uuid.UUID) (map[string]interface{}, error) {
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, fmt.Errorf("batch not found: %s", batchID)
	}

	progressPercent := 0.0
	if batch.TotalFiles > 0 {
		progressPercent = float64(batch.ProcessedFiles) / float64(batch.TotalFiles) * 100
	}

	progress := map[string]interface{}{
		"batch_id":         batch.ID,
		"status":           batch.Status,
		"total_files":      batch.TotalFiles,
		"processed_files":  batch.ProcessedFiles,
		"progress_percent": progressPercent,
		"created_at":       batch.CreatedAt,
		"updated_at":       batch.UpdatedAt,
	}

	if batch.CompletedAt != nil {
		progress["completed_at"] = batch.CompletedAt
	}

	return progress, nil
}

func serialize(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

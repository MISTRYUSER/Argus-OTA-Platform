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
	batchRepo  		domain.BatchRepository
	reportRepo		domain.ReportRepository
	cache			*redis.RedisClient
	sf				singleflight.Group
}
func NewQueryService(
	batchRepo		domain.BatchRepository,
	reportRepo		domain.ReportRepository,
	cache			*redis.RedisClient,
) *QueryService {
	return &QueryService{ 
		batchRepo:  batchRepo,
		reportRepo: reportRepo,
		cache: 		cache,
	}
}
// GetReport 获取报告（使用 Singleflight 防缓存击穿）
//
// 面试考点：
// Q: Singleflight 如何防止缓存击穿？
// A: 100 个并发请求查询同一个 batchID，sf.Do() 会将它们合并为 1 次执行
func (s *QueryService) GetReport(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
    key := batchID.String()

    v, err, shared := s.sf.Do(key, func() (interface{}, error) {
        log.Printf("[QueryService] Singleflight executing, key=%s", key)

        // 1. 先查缓存
        report, err := s.getReportFromCache(ctx, batchID)
        if err == nil && report != nil {
            log.Printf("[QueryService] Cache HIT: batchID=%s", batchID)
            return report, nil
        }

        log.Printf("[QueryService] Cache MISS: batchID=%s, querying database...", batchID)

        // 2. 缓存未命中，查数据库
        report, err = s.getReportFromDatabase(ctx, batchID)
        if err != nil {
            return nil, fmt.Errorf("failed to get report from database: %w", err)
        }

        // 3. 写入缓存
        if err := s.setReportToCache(ctx, report, 10*time.Minute); err != nil {
            log.Printf("[QueryService] Warning: failed to set cache: %v", err)
        }

        return report, nil
    })

    if err != nil {
        return nil, err
    }

    // shared = true 表示这个请求的结果被其他请求共享了
    if shared {
        log.Printf("[QueryService] Request was shared (merged with other concurrent requests)")
    }

    return v.(*domain.Report), nil
}

// getReportFromCache 从缓存获取报告（私有方法）
func (s *QueryService) getReportFromCache(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
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
	key := fmt.Sprintf("report:%s", report.BatchID)

	// 1. 序列化 Report → JSON
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	// 2. 写入 Redis（带过期时间）
	return s.cache.SET(ctx, key, string(data), ttl)
}
func (s *QueryService) getReportFromDatabase(ctx context.Context, batchID uuid.UUID) (*domain.Report,error) {
	report , err :=s.reportRepo.FindByBatchID(ctx,batchID)
	if err == nil && report != nil {
		return report,nil
	}

	batch,err := s.batchRepo.FindByID(ctx,batchID)
	if err != nil {
		return nil,fmt.Errorf("batch not found : %w",err)
	}

	report = domain.NewReport(batch)
	if err := s.reportRepo.Save(ctx,report);err != nil {
		log.Printf("[QueryService] Warning: failed to save report: %v", err)
	}
	return report,err
}
func (s *QueryService) GetProgress(ctx context.Context, batchID uuid.UUID) (map[string]interface{}, error) {
	batch, err := s.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return nil, err
	}

	progress := map[string]interface{}{
		"batch_id":        batch.ID,
		"status":          batch.Status,
		"total_files":     batch.TotalFiles,
		"processed_files": batch.ProcessedFiles,
		"progress_percent": float64(batch.ProcessedFiles) / float64(batch.TotalFiles) * 100,
		"created_at":      batch.CreatedAt,
		"updated_at":      batch.UpdatedAt,
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

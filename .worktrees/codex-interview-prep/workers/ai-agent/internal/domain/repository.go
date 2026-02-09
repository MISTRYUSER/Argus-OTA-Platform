package domain

import "context"

// DiagnosisRepository 诊断仓储接口（Domain 层定义）
type DiagnosisRepository interface {
	// GetAggregatedData 获取聚合数据（用于 DataLoader Node）
	GetAggregatedData(ctx context.Context, batchID string) (*AggregatedData, error)

	// Save 保存诊断（新增或更新）
	Save(ctx context.Context, diagnose *Diagnosis) error

	// FindByBatchID 根据 BatchID 查询诊断
	FindByBatchID(ctx context.Context, batchID string) (*Diagnosis, error)

	// FindByID 根据 ID 查询诊断
	FindByID(ctx context.Context, id string) (*Diagnosis, error)

	// FindSimilar 查找相似诊断（RAG，基于向量相似度）
	FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*Diagnosis, error)
}

// EventPublisher 事件发布器接口（用于 SSE）
type EventPublisher interface {
	// PublishProgress 推送实时进度
	PublishProgress(ctx context.Context, batchID string, event StreamEvent) error
}

package domain

import "context"

// SearchParams 检索参数（用于混合检索）
type SearchParams struct {
	ErrorCodes    []string // 错误码列表（用于精确过滤）
	EmbeddingText string   // 向量检索的查询文本
	TopK          int      // 返回结果数量
}

// VectorRetriever - 向量检索器接口（依赖倒置）
type VectorRetriever interface {
	// Search 混合检索（错误码过滤 + 向量排序）
	Search(ctx context.Context, params SearchParams) ([]SimilarCase, error)

	// Retrieve 检索相似案例（向后兼容）
	Retrieve(ctx context.Context, query string, topK int) ([]SimilarCase, error)

	// Index 索引新的诊断案例（用于增量更新）
	Index(ctx context.Context, diagnosis *Diagnosis) error
}

package nodes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// RAGNode 混合检索 Node
//
// 职责：
// 1. 错误码精确过滤（减少向量搜索范围）
// 2. 向量排序（语义相似度）
// 3. 降级处理（RAGUnavailable 标记）
type RAGNode struct {
	vectorRetriever domain.VectorRetriever
}

// NewRAGNode 创建 RAG Node
func NewRAGNode(vectorRetriever domain.VectorRetriever) *RAGNode {
	return &RAGNode{
		vectorRetriever: vectorRetriever,
	}
}

// Transform 实现 Eino Node 接口
func (n *RAGNode) Transform(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	// 1. 提取错误码列表
	errorCodes := extractErrorCodes(input.AggregatedData)

	// 2. 混合检索：错误码过滤 + 向量排序
	cases, err := n.vectorRetriever.Search(ctx, domain.SearchParams{
		ErrorCodes:   errorCodes,
		EmbeddingText: input.AggregatedData.LogsSummary,
		TopK:          5, // 只检索 Top-5
	})

	// 3. 降级处理
	if err != nil {
		// 📌 P1 改进：标记 RAG 不可用，而不是返回空列表
		input.RAGCases = []domain.SimilarCase{}
		input.RAGUnavailable = true
		input.ProcessingStatus = domain.StatusProcessing // 继续流程，让 LLM 降级处理

		return input, fmt.Errorf("RAG failed but continuing: %w", err)
	}

	// 4. 填充检索结果
	input.RAGCases = cases
	input.RAGUnavailable = false

	return input, nil
}

// extractErrorCodes 从聚合数据中提取错误码
func extractErrorCodes(data *domain.AggregatedData) []string {
	if data == nil || data.ErrorCodeStats == nil {
		return []string{}
	}

	codes := make([]string, 0, len(data.ErrorCodeStats))
	for code := range data.ErrorCodeStats {
		codes = append(codes, code)
	}

	return codes
}

// GraphNode 返回 Eino Chain 可用的 Lambda Node
func (n *RAGNode) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return n.Transform(ctx, input)
	})
}

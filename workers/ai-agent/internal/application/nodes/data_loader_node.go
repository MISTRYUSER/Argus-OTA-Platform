package nodes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// DataLoaderNode 从数据库加载聚合数据
//
// 职责：
// 1. 查询批次信息
// 2. 聚合错误码统计
// 3. 提取日志摘要（Token 截断优化）
type DataLoaderNode struct {
	diagnosisRepo domain.DiagnosisRepository
}

// NewDataLoaderNode 创建 DataLoader Node
func NewDataLoaderNode(diagnosisRepo domain.DiagnosisRepository) *DataLoaderNode {
	return &DataLoaderNode{
		diagnosisRepo: diagnosisRepo,
	}
}

// Transform 实现 Eino Node 接口
func (n *DataLoaderNode) Transform(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	// 1. 从数据库加载聚合数据
	aggData, err := n.diagnosisRepo.GetAggregatedData(ctx, input.BatchID)
	if err != nil {
		input.ProcessingStatus = domain.StatusFailed
		input.ErrorMessage = fmt.Sprintf("failed to load aggregated data: %v", err)
		return input, err
	}

	// 2. 填充聚合数据
	input.AggregatedData = aggData

	// 3. 📌 P2 改进：Token 截断优化（保留首尾日志）
	input.AggregatedData.LogsSummary = truncateLogs(aggData.RawLogs, 2000)

	// 4. 更新状态
	input.ProcessingStatus = domain.StatusProcessing

	return input, nil
}

// truncateLogs 截断日志（保留首尾，避免丢失关键信息）
//
// 📌 P2 改进：不要只保留前 N 条，而是保留前 1000 字符 + 后 1000 字符
func truncateLogs(rawLogs string, maxLen int) string {
	if len(rawLogs) <= maxLen {
		return rawLogs
	}

	// 保留前 60% + 后 40%
	frontLen := maxLen * 3 / 5
	backLen := maxLen * 2 / 5

	return rawLogs[:frontLen] +
		"\n... [省略中间日志] ...\n" +
		rawLogs[len(rawLogs)-backLen:]
}

// GraphNode 返回 Eino Chain 可用的 Lambda Node
func (n *DataLoaderNode) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return n.Transform(ctx, input)
	})
}

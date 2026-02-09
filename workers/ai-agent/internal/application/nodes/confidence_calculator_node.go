package nodes

import (
	"context"
	"fmt"
	"math"

	"github.com/cloudwego/eino/compose"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// ConfidenceCalculatorNode 置信度计算节点
//
// 📌 职责：
// 1. 分析错误码分布
// 2. 计算置信度（0-1）
// 3. 为 Supervisor 提供路由依据
//
// 📌 置信度规则：
// - 已知高频错误码（如 E001, E002）→ 0.8-0.95
// - 未知错误码 → 0.3-0.5
// - 错误码数量多（>10 个）→ 0.6-0.7
// - 日志数量少（< 100）→ 降低置信度
type ConfidenceCalculatorNode struct {
	knownErrorCodes map[string]struct{}
}

// NewConfidenceCalculatorNode 创建置信度计算节点
func NewConfidenceCalculatorNode(knownCodes []string) *ConfidenceCalculatorNode {
	codesMap := map[string]struct{}{
		"E001": {}, // CPU 过热
		"E002": {}, // 内存泄漏
		"E003": {}, // CAN 总线超时
		"E004": {}, // 传感器故障
		"E005": {}, // 电池电量过低
		"E006": {}, // 网络超时
		"E007": {}, // 磁盘 IO 错误
		"E008": {}, // 固件版本不匹配
		"E009": {}, // 配置错误
		"E010": {}, // 权限不足
	}
	for _, c := range knownCodes {
		codesMap[c] = struct{}{}
	}
	return &ConfidenceCalculatorNode{
		knownErrorCodes: codesMap,
	}
}

// Transform 实现 Eino Node 接口
func (n *ConfidenceCalculatorNode) Transform(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	// 1. 基础置信度
	confidence := 0.5

	// 2. 解析错误码
	if input.AggregatedData != nil && input.AggregatedData.ErrorCodeStats != nil {
		errorCodeStats := input.AggregatedData.ErrorCodeStats

		// 2.1 错误码数量因子
		numCodes := len(errorCodeStats)
		if numCodes > 10 {
			// 错误码太多，降低置信度
			confidence -= 0.1
		} else if numCodes <= 3 {
			confidence += 0.1
		}

		// 2.2 已知/未知错误码统计
		knownCount := 0
		unknownCount := 0
		totalErrors := 0

		for code, count := range errorCodeStats {
			totalErrors += count
			if _, exists := n.knownErrorCodes[code]; exists {
				knownCount++
			} else {
				unknownCount++
			}
		}

		// 2.3 已知错误码比例因子
		if numCodes > 0 {
			knownRatio := float64(knownCount) / float64(numCodes)
			if knownRatio > 0.8 {
				// 大部分是已知错误码，提高置信度
				confidence += 0.25
			} else if knownRatio < 0.3 {
				// 大部分是未知错误码，降低置信度
				confidence -= 0.2
			}
		}

		// 2.4 高频错误码因子
		maxCount := 0
		for _, count := range errorCodeStats {
			if count > maxCount {
				maxCount = count
			}
		}
		if totalErrors > 0 {
			dominantRatio := float64(maxCount) / float64(totalErrors)
			if dominantRatio > 0.8 {
				// 单一错误码占主导，提高置信度
				confidence += 0.2
			}
		}
	}

	// 3. 确保置信度在 [0, 1] 范围内
	confidence = math.Max(0.0, math.Min(1.0, confidence))

	// 4. 设置置信度到上下文
	input.Confidence = confidence

	// 5. 记录日志
	decision := "high_confidence"
	if confidence < 0.7 {
		decision = "low_confidence"
	}
	fmt.Printf("[ConfidenceCalculator] Confidence: %.2f → Decision: %s\n", confidence, decision)

	return input, nil
}

// GraphNode 返回 Eino Chain 可用的 Lambda Node
func (n *ConfidenceCalculatorNode) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return n.Transform(ctx, input)
	})
}

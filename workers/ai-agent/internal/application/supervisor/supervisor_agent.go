// Package supervisor 实现 Supervisor-Worker 架构
//
// 核心理念：
//   - Supervisor（中控）持有全局状态，动态决策调用哪个 Worker
//   - Workers（专家）执行具体任务，完成后将控制权交还给 Supervisor
//   - 实现"控制权回收"机制，避免 Worker 之间"踢皮球"
//
// 数据流向：Kafka Event -> Supervisor -> Worker -> Supervisor -> Decision -> ...
//                                            (控制权回收)
package supervisor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// SupervisorAgent 中央调度器
//
// 职责：
//   1. 维护全局状态 (DiagnosisContext)
//   2. 根据状态动态决策调用哪个 Worker
//   3. 接收 Worker 返回结果，决定下一步
//   4. 防止无限循环和死锁
type SupervisorAgent struct {
	// 决策阈值
	HighConfidenceThreshold float64 // 高置信度阈值（直接输出）
	LowConfidenceThreshold  float64 // 低置信度阈值（需要 RAG）

	// 最大重试次数
	MaxRAGRetries int // RAG 检索最大重试次数

	// Workers 的引用（用于决策）
	availableWorkers map[string]string // Worker 名称 → 描述
}

// NewSupervisorAgent 创建 Supervisor Agent
func NewSupervisorAgent() *SupervisorAgent {
	return &SupervisorAgent{
		HighConfidenceThreshold: 0.8, // 置信度 > 0.8 直接输出
		LowConfidenceThreshold:  0.5, // 置信度 < 0.5 需要 RAG
		MaxRAGRetries:           2,   // 最多重试 2 次
		availableWorkers: map[string]string{
			"guard":     "执行合规性检查",
			"loader":    "加载数据",
			"rag":       "检索历史案例",
			"reasoning": "执行推理分析",
			"terminal":  "终止流程",
		},
	}
}

// Decision Supervisor 的决策结果
type Decision struct {
	NextWorker   string                   // 下一步调用的 Worker
	Instruction  string                   // 给 Worker 的指令
	ShouldEnd    bool                     // 是否结束流程
	EndReason    string                   // 结束原因
	UpdateState func(*domain.DiagnosisContext) // 状态更新函数
}

// Decide 核心决策逻辑
//
// 这是 Supervisor 的"大脑"，根据当前状态决定下一步
func (s *SupervisorAgent) Decide(ctx context.Context, state *domain.DiagnosisContext) *Decision {
	// 1. 检查是否应该终止（防止无限循环）
	if state.ShouldTerminate() {
		return &Decision{
			ShouldEnd: true,
			EndReason: fmt.Sprintf("Terminated: %s", state.ErrorMessage),
			NextWorker: "terminal",
		}
	}

	// 2. 根据当前 Worker 决策下一步
	switch state.LastWorker {
	case "":
		// ========== 初始状态：先执行 Guard ==========
		return &Decision{
			NextWorker:  "guard",
			Instruction: "检查请求是否合规",
		}

	case "guard":
		// ========== Guard 完成 ==========
		if state.ProcessingStatus == domain.StatusFailed {
			// Guard 失败，直接终止
			return &Decision{
				ShouldEnd: true,
				EndReason: fmt.Sprintf("Guard failed: %s", state.ErrorMessage),
				NextWorker: "terminal",
			}
		}
		// Guard 通过，加载数据
		return &Decision{
			NextWorker:  "loader",
			Instruction: "加载批次数据",
		}

	case "loader":
		// ========== 数据加载完成 ==========
		if state.ProcessingStatus == domain.StatusFailed {
			return &Decision{
				ShouldEnd: true,
				EndReason: fmt.Sprintf("Data loading failed: %s", state.ErrorMessage),
				NextWorker: "terminal",
			}
		}

		// 数据加载成功，根据置信度决策
		if state.Confidence >= s.HighConfidenceThreshold {
			// 高置信度：直接走推理（快通道）
			return &Decision{
				NextWorker:  "reasoning",
				Instruction: "高置信度场景，直接推理分析",
			}
		}

		// 低/中置信度：需要 RAG 检索
		return &Decision{
			NextWorker:  "rag",
			Instruction: "置信度不足，检索相似历史案例",
		}

	case "rag":
		// ========== RAG 检索完成 ==========
		if state.RAGUnavailable {
			// RAG 不可用，降级到推理
			return &Decision{
				NextWorker:  "reasoning",
				Instruction: "RAG 不可用，使用通用推理",
			}
		}

		if len(state.RAGCases) == 0 {
			// RAG 没查到，可以重试或降级
			if state.CurrentIteration <= s.MaxRAGRetries {
				return &Decision{
					NextWorker:  "rag",
					Instruction: "未检索到案例，使用不同关键词重试",
				}
			}
			// 达到重试上限，降级
			return &Decision{
				NextWorker:  "reasoning",
				Instruction: "RAG 重试失败，使用通用推理",
			}
		}

		// RAG 成功，进入推理
		return &Decision{
			NextWorker:  "reasoning",
			Instruction: fmt.Sprintf("基于 %d 条相似案例进行推理", len(state.RAGCases)),
		}

	case "reasoning":
		// ========== 推理完成 ==========
		if state.ProcessingStatus == domain.StatusFailed {
			// 推理失败，可以尝试重试或结束
			if state.CurrentIteration < state.MaxIterations {
				return &Decision{
					NextWorker:  "reasoning",
					Instruction: "推理失败，使用不同策略重试",
				}
			}
			return &Decision{
				ShouldEnd: true,
				EndReason: fmt.Sprintf("Reasoning failed after retries: %s", state.ErrorMessage),
				NextWorker: "terminal",
			}
		}

		// 推理成功，检查置信度
		if state.DiagnosisResult != nil && state.DiagnosisResult.Confidence < 0.5 {
			// 置信度仍然很低，可能需要重新 RAG 或结束
			if state.LastWorker == "rag" {
				// 已经做过 RAG，置信度还是低，只能接受
				return &Decision{
					ShouldEnd: true,
					EndReason: "Low confidence diagnosis (accepted after RAG)",
					NextWorker: "terminal",
				}
			}
			// 还没做过 RAG，去检索
			return &Decision{
				NextWorker:  "rag",
				Instruction: "推理置信度低，检索相似案例辅助",
			}
		}

		// 推理成功且置信度可接受，结束
		return &Decision{
			ShouldEnd: true,
			EndReason: "Diagnosis completed successfully",
			NextWorker: "terminal",
		}

	default:
		// ========== 未知状态 ==========
		return &Decision{
			ShouldEnd: true,
			EndReason: fmt.Sprintf("Unknown worker: %s", state.LastWorker),
			NextWorker: "terminal",
		}
	}
}

// GeneratePrompt 生成 Supervisor 的决策 Prompt
//
// 这是 LLM 驱动的决策模式（可选）
func (s *SupervisorAgent) GeneratePrompt(state *domain.DiagnosisContext) string {
	var sb strings.Builder

	sb.WriteString("## Supervisor 决策上下文\n\n")
	sb.WriteString(fmt.Sprintf("当前迭代: %d/%d\n", state.CurrentIteration, state.MaxIterations))
	sb.WriteString(fmt.Sprintf("上一个 Worker: %s\n", state.LastWorker))
	sb.WriteString(fmt.Sprintf("当前状态: %s\n\n", state.ProcessingStatus))

	if state.Confidence > 0 {
		sb.WriteString(fmt.Sprintf("置信度: %.2f\n", state.Confidence))
	}

	if len(state.RAGCases) > 0 {
		sb.WriteString(fmt.Sprintf("RAG 案例: %d 条\n", len(state.RAGCases)))
	}

	sb.WriteString("\n### 可用 Workers\n")
	for name, desc := range s.availableWorkers {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", name, desc))
	}

	sb.WriteString("\n### 执行历史\n")
	for i, worker := range state.WorkerHistory {
		if i > 0 && i%5 == 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(worker + " → ")
	}

	return sb.String()
}

// ========================================================================
// Supervisor Agent 作为 Eino Lambda
// ========================================================================

// NewSupervisorRunnable 创建 Supervisor 的 Lambda
//
// 这是 Supervisor 的核心执行逻辑：
//   1. 接收 State
//   2. 调用 Decide() 决策
//   3. 返回更新后的 State（包含 NextWorker 信息）
func NewSupervisorRunnable(supervisor *SupervisorAgent) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		// 记录决策时间
		startTime := time.Now()

		// 调用决策逻辑
		decision := supervisor.Decide(ctx, state)

		// 更新状态
		if decision.UpdateState != nil {
			decision.UpdateState(state)
		}

		// 设置下一步
		state.NextStep = decision.NextWorker
		state.Instruction = decision.Instruction

		if decision.ShouldEnd {
			state.ProcessingStatus = domain.StatusSuccess
			state.NextStep = compose.END
		}

		// 记录 Supervisor 执行时间
		if state.WorkerTimings == nil {
			state.WorkerTimings = make(map[string]time.Duration)
		}
		state.WorkerTimings["supervisor"] = time.Since(startTime)

		// 日志
		fmt.Printf("[Supervisor] Decision: %s → %s (%s)\n",
			state.LastWorker, decision.NextWorker, decision.Instruction)

		return state, nil
	})
}

// ========================================================================
// LLM-Driven Supervisor（可选，更高级的模式）
// ========================================================================

// LLMDecide 使用 LLM 进行决策（适用于复杂场景）
func (s *SupervisorAgent) LLMDecide(
	ctx context.Context,
	llm interface{}, // 简化类型定义，实际使用时传入具体的 LLM 实现
	state *domain.DiagnosisContext,
) (*Decision, error) {
	// 简化实现，直接使用规则决策
	return s.Decide(ctx, state), nil
}

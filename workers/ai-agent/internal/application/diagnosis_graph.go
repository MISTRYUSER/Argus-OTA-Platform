package application

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/nodes"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// DiagnosisGraph 诊断流程图（完整版）
//
// 完整流程：
//   START → hard_guards → [reject/notify/schedule] → END
//         │ passed
//         ▼
//   data_loader → confidence → [决策分支]
//                        │
//           confidence < 0.7 ───┤
//           confidence ≥ 0.7 ───┤
//               │                  │
//               ▼                  ▼
//         [RAG → LLM]          [LLM]
//               │                  │
//               └──────────────────┘
//               │
//               ▼
//             END
type DiagnosisGraph struct {
	dataLoaderNode     *nodes.DataLoaderNode
	confidenceCalcNode *nodes.ConfidenceCalculatorNode
	ragNode            *nodes.RAGNode
	llmNode            *nodes.LLMNode
	runnable           compose.Runnable[*domain.DiagnosisContext, *domain.DiagnosisContext]
}

// NewDiagnosisGraph 创建诊断流程图
func NewDiagnosisGraph(
	diagnosisRepo domain.DiagnosisRepository,
	vectorRetriever domain.VectorRetriever,
	llmConfig *llm.GLM4Config,
) (*DiagnosisGraph, error) {
	// 1. 创建 LLM Chat Model
	chatModel, err := llm.NewGLM4ChatModel(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM chat model: %w", err)
	}

	// 2. 创建 Worker Nodes
	dataLoaderNode := nodes.NewDataLoaderNode(diagnosisRepo)
	confidenceCalcNode := nodes.NewConfidenceCalculatorNode(nil)
	ragNode := nodes.NewRAGNode(vectorRetriever)
	llmNode := nodes.NewLLMNode(chatModel)

	// 3. 创建 Graph
	graph := compose.NewGraph[*domain.DiagnosisContext, *domain.DiagnosisContext](
		compose.WithGenLocalState(newStateGenerator()),
	)

	// ============================================================
	// 4. hard_guards 节点（硬约束）
	// ============================================================

	guardsNode := compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		result := RunGuards(ctx, state)

		if !result.Passed {
			state.ProcessingStatus = domain.StatusFailed
			state.ErrorMessage = fmt.Sprintf("Guard '%s' failed: %s", result.Blocked.RuleName, result.Blocked.Reason)
			fmt.Printf("[Guard] ✋ Rule '%s' blocked: %s\n", result.Blocked.RuleName, result.Blocked.Reason)

			// 根据 Action 决定下一步
			switch result.Blocked.Action {
			case GuardActionReject:
				state.NextStep = "reject"
			case GuardActionNotify:
				state.NextStep = "notify"
			case GuardActionSchedule:
				state.NextStep = "schedule"
			default:
				state.NextStep = "reject"
			}
			return state, nil
		}

		fmt.Println("[Guard] ✅ All passed")
		state.NextStep = "data_loader"  // 通过 Guards，进入数据加载
		return state, nil
	})

	err = graph.AddLambdaNode("hard_guards", guardsNode)
	if err != nil {
		return nil, fmt.Errorf("failed to add hard_guards node: %w", err)
	}

	// ============================================================
	// 5. data_loader 节点（加载数据）
	// ============================================================

	err = graph.AddLambdaNode("data_loader", dataLoaderNode.GraphNode())
	if err != nil {
		return nil, fmt.Errorf("failed to add data_loader node: %w", err)
	}

	// ============================================================
	// 6. confidence 节点（计算置信度）
	// ============================================================

	err = graph.AddLambdaNode("confidence", confidenceCalcNode.GraphNode())
	if err != nil {
		return nil, fmt.Errorf("failed to add confidence node: %w", err)
	}

	// ============================================================
	// 7. 诊断分支（根据置信度选择路径）
	// ============================================================
	//
	// 低置信度路径：confidence → rag → llm
	// 高置信度路径：confidence → llm
	//
	// 需要添加的节点：
	//   - rag (低置信度时使用)
	//   - llm (所有路径都使用)

	// 添加 RAG 节点
	err = graph.AddLambdaNode("rag", ragNode.GraphNode())
	if err != nil {
		return nil, fmt.Errorf("failed to add rag node: %w", err)
	}

	// 添加 LLM 节点
	err = graph.AddLambdaNode("llm", llmNode.GraphNode())
	if err != nil {
		return nil, fmt.Errorf("failed to add llm node: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to add confidence branch: %w", err)
	}

	// ============================================================
	// 8. 终端节点（reject/notify/schedule → END）
	// ============================================================

	rejectNode := compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		fmt.Printf("[Terminal] ❌ Rejected: %s\n", state.ErrorMessage)
		return state, nil
	})
	err = graph.AddLambdaNode("reject", rejectNode)
	if err != nil {
		return nil, fmt.Errorf("failed to add reject node: %w", err)
	}

	notifyNode := compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		fmt.Printf("[Terminal] 🔔 Notify user: %s\n", state.ErrorMessage)
		return state, nil
	})
	err = graph.AddLambdaNode("notify", notifyNode)
	if err != nil {
		return nil, fmt.Errorf("failed to add notify node: %w", err)
	}

	scheduleNode := compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		fmt.Printf("[Terminal] 📅 Scheduled for retry: %s\n", state.ErrorMessage)
		return state, nil
	})
	err = graph.AddLambdaNode("schedule", scheduleNode)
	if err != nil {
		return nil, fmt.Errorf("failed to add schedule node: %w", err)
	}

	// ============================================================
	// 9. 添加边
	// ============================================================

	// START → hard_guards
	err = graph.AddEdge(compose.START, "hard_guards")
	if err != nil {
		return nil, fmt.Errorf("failed to add edge START->hard_guards: %w", err)
	}

	// hard_guards → [data_loader, reject, notify, schedule]
	guardsBranch := compose.NewGraphBranch(
		func(ctx context.Context, state *domain.DiagnosisContext) (string, error) {
			return state.NextStep, nil
		},
		map[string]bool{
			"data_loader": true,
			"reject":       true,
			"notify":       true,
			"schedule":     true,
		},
	)
	err = graph.AddBranch("hard_guards", guardsBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to add guards branch: %w", err)
	}

	// data_loader → confidence
	err = graph.AddEdge("data_loader", "confidence")
	if err != nil {
		return nil, fmt.Errorf("failed to add edge data_loader->confidence: %w", err)
	}

	// confidence → [rag, llm] based on confidence level
	confidenceRouteBranch := compose.NewGraphBranch(
		func(ctx context.Context, state *domain.DiagnosisContext) (string, error) {
			if state.Confidence < 0.7 {
				fmt.Printf("[Branch] Low confidence (%.2f) → RAG path\n", state.Confidence)
				return "rag", nil
			}
			fmt.Printf("[Branch] High confidence (%.2f) → Fast path\n", state.Confidence)
			return "llm", nil
		},
		map[string]bool{
			"rag": true,
			"llm": true,
		},
	)
	err = graph.AddBranch("confidence", confidenceRouteBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to add confidence route branch: %w", err)
	}

	// rag → llm (低置信度路径：RAG 后走 LLM)
	err = graph.AddEdge("rag", "llm")
	if err != nil {
		return nil, fmt.Errorf("failed to add edge rag->llm: %w", err)
	}

	// llm → END (所有路径最终汇合到 LLM 后结束)
	err = graph.AddEdge("llm", compose.END)
	if err != nil {
		return nil, fmt.Errorf("failed to add edge llm->END: %w", err)
	}

	// reject/notify/schedule → END
	err = graph.AddEdge("reject", compose.END)
	if err != nil {
		return nil, fmt.Errorf("failed to add edge reject->END: %w", err)
	}
	err = graph.AddEdge("notify", compose.END)
	if err != nil {
		return nil, fmt.Errorf("failed to add edge notify->END: %w", err)
	}
	err = graph.AddEdge("schedule", compose.END)
	if err != nil {
		return nil, fmt.Errorf("failed to add edge schedule->END: %w", err)
	}

	// ============================================================
	// 10. 编译 Graph
	// ============================================================

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to compile graph: %w", err)
	}

	return &DiagnosisGraph{
		dataLoaderNode:     dataLoaderNode,
		confidenceCalcNode: confidenceCalcNode,
		ragNode:            ragNode,
		llmNode:            llmNode,
		runnable:           runnable,
	}, nil
}

// Run 运行诊断流程
func (g *DiagnosisGraph) Run(ctx context.Context, batchID string) (*domain.DiagnosisResult, error) {
	// 1. 创建初始状态
	state := &domain.DiagnosisContext{
		BatchID:          batchID,
		ProcessingStatus: domain.StatusPending,
		NextStep:         "hard_guards",
	}

	// 2. 执行 Graph
	output, err := g.runnable.Invoke(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("graph execution failed: %w", err)
	}

	// 3. 检查结果
	if output.DiagnosisResult != nil {
		return output.DiagnosisResult, nil
	}

	return nil, fmt.Errorf("no diagnosis result: %s", output.ErrorMessage)
}

// ============================================================
// 辅助函数
// ============================================================

func newStateGenerator() func(ctx context.Context) *domain.DiagnosisContext {
	return func(ctx context.Context) *domain.DiagnosisContext {
		return &domain.DiagnosisContext{
			ProcessingStatus: domain.StatusPending,
			NextStep:         "hard_guards",
		}
	}
}

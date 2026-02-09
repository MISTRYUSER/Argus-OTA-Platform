// Package supervisor 实现 Supervisor-Worker Graph 编排
//
// 核心思想：
//   - Supervisor 是一个有状态的 Agent，持有全局 DiagnosisContext
//   - Workers 是无状态的执行单元，执行完后将控制权交还给 Supervisor
//   - 通过循环边（Loop Edge）实现 Supervisor 到 Worker 到 Supervisor 的决策循环
//
// 数据流向：
//   START -> Supervisor -> Worker -> Supervisor -> Decision -> ...
//                                         (控制权回收)
package supervisor

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// SupervisorGraph Supervisor-Worker 编排图
//
// 这是整个系统的"大脑"，负责：
//   1. 维护全局状态 (DiagnosisContext)
//   2. 动态调度 Workers
//   3. 防止无限循环
//   4. 决定何时结束流程
type SupervisorGraph struct {
	supervisor *SupervisorAgent
	workers    map[string]Worker
	graph      compose.Runnable[*domain.DiagnosisContext, *domain.DiagnosisContext]
}

// NewSupervisorGraph 创建 Supervisor-Worker Graph
//
// 这是构建 Supervisor 模式的入口函数
func NewSupervisorGraph(
	diagnosisRepo domain.DiagnosisRepository,
	vectorRetriever domain.VectorRetriever,
	llmConfig *llm.GLM4Config,
) (*SupervisorGraph, error) {
	// ============================================================
	// 1. 创建 Supervisor Agent（中控）
	// ============================================================
	supervisor := NewSupervisorAgent()

	// ============================================================
	// 2. 创建 Workers（专家）
	// ============================================================
	guardWorker := NewGuardWorker()
	loaderWorker := NewLoaderWorker(diagnosisRepo)
	ragWorker := NewRAGWorker(vectorRetriever)
	reasoningWorker, err := NewReasoningWorker(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create reasoning worker: %w", err)
	}
	terminalWorker := NewTerminalWorker()

	// ============================================================
	// 3. 注册所有 Workers
	// ============================================================
	workers := map[string]Worker{
		"guard":     guardWorker,
		"loader":    loaderWorker,
		"rag":       ragWorker,
		"reasoning": reasoningWorker,
		"terminal":  terminalWorker,
	}

	// ============================================================
	// 4. 构建 Graph
	// ============================================================
	graph := compose.NewGraph[*domain.DiagnosisContext, *domain.DiagnosisContext]()

	// ============================================================
	// 5. 添加 Supervisor 节点（决策中心）
	// ============================================================
	supervisorRunnable := NewSupervisorRunnable(supervisor)
	if err := graph.AddLambdaNode("supervisor", supervisorRunnable); err != nil {
		return nil, fmt.Errorf("failed to add supervisor node: %w", err)
	}

	// ============================================================
	// 6. 添加所有 Worker 节点
	// ============================================================
	for name, worker := range workers {
		if err := graph.AddLambdaNode(name, worker.GraphNode()); err != nil {
			return nil, fmt.Errorf("failed to add worker %s: %w", name, err)
		}
	}

	// ============================================================
	// 7. 添加边（实现 Supervisor → Worker → Supervisor 循环）
	// ============================================================

	// START → Supervisor
	if err := graph.AddEdge(compose.START, "supervisor"); err != nil {
		return nil, fmt.Errorf("failed to add edge START->supervisor: %w", err)
	}

	// Supervisor → Workers（动态分支）
	// 这是 Supervisor 模式的核心：Supervisor 决定下一步调用哪个 Worker
	supervisorBranch := compose.NewGraphBranch(
		func(ctx context.Context, state *domain.DiagnosisContext) (string, error) {
			// 检查是否应该结束
			if state.NextStep == compose.END || state.ShouldTerminate() {
				return "terminal", nil
			}
			// 返回 Supervisor 决定的 Worker
			return state.NextStep, nil
		},
		map[string]bool{
			"guard":     true,
			"loader":    true,
			"rag":       true,
			"reasoning": true,
			"terminal":  true,
		},
	)
	if err := graph.AddBranch("supervisor", supervisorBranch); err != nil {
		return nil, fmt.Errorf("failed to add supervisor branch: %w", err)
	}

	// ============================================================
	// 8. 添加"控制权回收"边（Workers → Supervisor）
	// ============================================================
	// 这是 Supervisor 模式的关键：每个 Worker 执行完后，都必须回到 Supervisor
	// 这样 Supervisor 才能根据执行结果做出下一步决策
	for _, workerName := range []string{"guard", "loader", "rag", "reasoning"} {
		if err := graph.AddEdge(workerName, "supervisor"); err != nil {
			return nil, fmt.Errorf("failed to add edge %s->supervisor: %w", workerName, err)
		}
	}

	// terminal → END
	if err := graph.AddEdge("terminal", compose.END); err != nil {
		return nil, fmt.Errorf("failed to add edge terminal->END: %w", err)
	}

	// ============================================================
	// 9. 编译 Graph
	// ============================================================
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to compile graph: %w", err)
	}

	return &SupervisorGraph{
		supervisor: supervisor,
		workers:    workers,
		graph:      runnable,
	}, nil
}

// ========================================================================
// 执行方法
// ========================================================================

// Run 运行 Supervisor-Worker 流程
//
// 这是外部调用的入口
func (g *SupervisorGraph) Run(ctx context.Context, batchID string) (*domain.DiagnosisResult, error) {
	// 创建初始状态
	state := domain.NewDiagnosisContext(batchID)

	// 执行 Graph
	output, err := g.graph.Invoke(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("graph execution failed: %w", err)
	}

	// 返回结果
	if output.DiagnosisResult != nil {
		return output.DiagnosisResult, nil
	}

	if output.ProcessingStatus == domain.StatusFailed {
		return nil, fmt.Errorf("diagnosis failed: %s", output.ErrorMessage)
	}

	return nil, fmt.Errorf("no diagnosis result produced")
}

// ========================================================================
// 可视化辅助
// ========================================================================

// PrintGraph 打印 Graph 结构（用于调试）
func (g *SupervisorGraph) PrintGraph() {
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║         Supervisor-Worker Architecture (数据流向)            ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("                    ┌─────────────────┐")
	fmt.Println("                    │   START         │")
	fmt.Println("                    └────────┬────────┘")
	fmt.Println("                             │")
	fmt.Println("                             ▼")
	fmt.Println("                    ┌─────────────────┐")
	fmt.Println("                    │  SUPERVISOR     │ ◄───────┐")
	fmt.Println("                    │  (决策中心)      │         │")
	fmt.Println("                    └────────┬────────┘         │")
	fmt.Println("                             │                  │")
	fmt.Println("              ┌──────────────┼──────────────┐ │")
	fmt.Println("              ▼              ▼              ▼ │")
	fmt.Println("         ┌─────────┐   ┌─────────┐   ┌─────────┐   │")
	fmt.Println("         │  Guard  │   │  Loader │   │   RAG   │   │")
	fmt.Println("         └────┬────┘   └────┬────┘   └────┬────┘   │")
	fmt.Println("              │             │             │        │")
	fmt.Println("              └──────────────┼──────────────┘        │")
	fmt.Println("                             │                       │")
	fmt.Println("                             ▼                       │")
	fmt.Println("                    ┌─────────────────┐              │")
	fmt.Println("                    │  Reasoning      │              │")
	fmt.Println("                    └────────┬────────┘              │")
	fmt.Println("                             │                       │")
	fmt.Println("                             └───────────────────────┘")
	fmt.Println("                             │")
	fmt.Println("                             ▼")
	fmt.Println("                    ┌─────────────────┐")
	fmt.Println("                    │   SUPERVISOR     │ ◄── 控制权回收")
	fmt.Println("                    │  (重新决策)      │")
	fmt.Println("                    └────────┬────────┘")
	fmt.Println("                             │")
	fmt.Println("                             ▼")
	fmt.Println("                    ┌─────────────────┐")
	fmt.Println("                    │   Terminal       │")
	fmt.Println("                    │   (输出/终止)     │")
	fmt.Println("                    └────────┬────────┘")
	fmt.Println("                             │")
	fmt.Println("                             ▼")
	fmt.Println("                    ┌─────────────────┐")
	fmt.Println("                    │   END           │")
	fmt.Println("                    └─────────────────┘")
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════════")
	fmt.Println("  核心特性：")
	fmt.Println("  1. Supervisor 持有全局状态 (DiagnosisContext)")
	fmt.Println("  2. 每个 Worker 执行完后必须返回 Supervisor")
	fmt.Println("  3. Supervisor 根据执行结果动态决策下一步")
	fmt.Println("  4. 防止无限循环 (MaxIterations + Loop Detection)")
	fmt.Println("════════════════════════════════════════════════════════════════")
}

// GetStats 获取执行统计信息
func (g *SupervisorGraph) GetStats(state *domain.DiagnosisContext) map[string]interface{} {
	return map[string]interface{}{
		"total_iterations": state.CurrentIteration,
		"worker_history":   state.WorkerHistory,
		"worker_timings":   state.WorkerTimings,
		"final_status":     state.ProcessingStatus,
		"final_confidence": state.Confidence,
		"total_time":       state.WorkerTimings["supervisor"],
	}
}

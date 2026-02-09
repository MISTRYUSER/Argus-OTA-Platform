package application

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/nodes"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// DiagnosisGraph 诊断流程图（Sequential Graph）
//
// 架构模式：Sequential Graph（线性流水线）
// 流程：DataLoader → RAG → LLM → END
//
// 📌 术语修正：不是 Supervisor-Worker，而是 RAG Pipeline
// - Supervisor 需要中心大脑动态决策（如 confidence < 0.5 时查 RAG）
// - 当前是固定流程：DataLoader → RAG → LLM
// - v2.0 会引入动态决策
type DiagnosisGraph struct {
	dataLoaderNode *nodes.DataLoaderNode
	ragNode        *nodes.RAGNode
	llmNode        *nodes.LLMNode
	runnable       compose.Runnable[*domain.DiagnosisContext, *domain.DiagnosisContext]
}

// NewDiagnosisGraph 创建诊断流程图
func NewDiagnosisGraph(
	diagnosisRepo domain.DiagnosisRepository,
	vectorRetriever domain.VectorRetriever,
	llmConfig *llm.GLM4Config,
) (*DiagnosisGraph, error) {
	// 1. 创建 LLM Chat Model（使用 Eino Model 接口）
	chatModel, err := llm.NewGLM4ChatModel(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM chat model: %w", err)
	}

	// 2. 创建 Nodes
	dataLoaderNode := nodes.NewDataLoaderNode(diagnosisRepo)
	ragNode := nodes.NewRAGNode(vectorRetriever)
	llmNode := nodes.NewLLMNode(chatModel)

	// 3. 构建 Sequential Graph
	// 📌 使用 Eino 的 Chain 构建线性流程
	chain := compose.NewChain[*domain.DiagnosisContext, *domain.DiagnosisContext]()

	// 添加 Nodes（按顺序执行）
	chain.
		AppendLambda(dataLoaderNode.GraphNode()).
		AppendLambda(ragNode.GraphNode()).
		AppendLambda(llmNode.GraphNode())

	// 4. 编译 Chain
	runnable, err := chain.Compile(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to compile chain: %w", err)
	}

	return &DiagnosisGraph{
		dataLoaderNode: dataLoaderNode,
		ragNode:        ragNode,
		llmNode:        llmNode,
		runnable:       runnable,
	}, nil
}

// Run 运行诊断流程
func (g *DiagnosisGraph) Run(ctx context.Context, batchID string) (*domain.DiagnosisResult, error) {
	// 1. 创建初始状态
	state := &domain.DiagnosisContext{
		BatchID:          batchID,
		ProcessingStatus: domain.StatusPending,
	}

	// 2. 执行 Graph
	output, err := g.runnable.Invoke(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("graph execution failed: %w", err)
	}

	// 3. 提取最终结果
	if output.ProcessingStatus != domain.StatusSuccess {
		return nil, fmt.Errorf("diagnosis failed: %s", output.ErrorMessage)
	}

	return output.DiagnosisResult, nil
}

package supervisor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// ========================================================================
// Workers 基础接口
// ========================================================================

// Worker 基础接口
type Worker interface {
	// Name 返回 Worker 名称
	Name() string

	// Execute 执行 Worker 逻辑
	Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error)

	// GraphNode 返回 Eino Lambda（用于 Graph 节点）
	GraphNode() *compose.Lambda
}

// BaseWorker 基础 Worker（提供通用能力）
type BaseWorker struct {
	name string
}

func (w *BaseWorker) Name() string {
	return w.name
}

// ========================================================================
// Guard Worker - 合规检查
// ========================================================================

// GuardWorker 执行硬约束检查
type GuardWorker struct {
	BaseWorker
}

// NewGuardWorker 创建 Guard Worker
func NewGuardWorker() *GuardWorker {
	return &GuardWorker{
		BaseWorker: BaseWorker{name: "guard"},
	}
}

func (w *GuardWorker) Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	startTime := time.Now()

	fmt.Printf("[GuardWorker] Starting guard checks for batch %s\n", state.BatchID)

	// 执行 Guard 检查
	result := RunGuardsForBatch(state)

	// 更新状态
	state.RecordWorker(w.Name())

	if !result.Passed {
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = fmt.Sprintf("Guard '%s' blocked: %s", result.RuleName, result.Reason)
		fmt.Printf("[GuardWorker] ❌ Blocked by '%s': %s\n", result.RuleName, result.Reason)
	} else {
		fmt.Println("[GuardWorker] ✅ All guards passed")
	}

	// 记录执行时间
	state.WorkerTimings[w.Name()] = time.Since(startTime)

	return state, nil
}

func (w *GuardWorker) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return w.Execute(ctx, state)
	})
}

// ========================================================================
// Loader Worker - 数据加载
// ========================================================================

// LoaderWorker 加载批次数据
type LoaderWorker struct {
	BaseWorker
	repo domain.DiagnosisRepository
}

// NewLoaderWorker 创建 Loader Worker
func NewLoaderWorker(repo domain.DiagnosisRepository) *LoaderWorker {
	return &LoaderWorker{
		BaseWorker: BaseWorker{name: "loader"},
		repo:       repo,
	}
}

func (w *LoaderWorker) Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	startTime := time.Now()

	fmt.Printf("[LoaderWorker] Loading data for batch %s\n", state.BatchID)

	// 加载数据
	data, err := w.repo.GetAggregatedData(ctx, state.BatchID)
	if err != nil {
		state.RecordWorker(w.Name())
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = fmt.Sprintf("Failed to load data: %v", err)
		return state, nil
	}

	if data == nil {
		state.RecordWorker(w.Name())
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = fmt.Sprintf("Batch %s not found", state.BatchID)
		return state, nil
	}

	// 更新状态
	state.AggregatedData = data
	state.ProcessingStatus = domain.StatusProcessing
	state.RecordWorker(w.Name())

	// 计算初始置信度
	state.Confidence = w.calculateConfidence(data)

	fmt.Printf("[LoaderWorker] ✅ Data loaded, confidence: %.2f\n", state.Confidence)

	// 记录执行时间
	state.WorkerTimings[w.Name()] = time.Since(startTime)

	return state, nil
}

// calculateConfidence 根据数据质量计算初始置信度
func (w *LoaderWorker) calculateConfidence(data *domain.AggregatedData) float64 {
	if data == nil {
		return 0.0
	}

	confidence := 0.5 // 基础置信度

	if len(data.ErrorCodeStats) > 0 {
		confidence += 0.1
	}

	if data.LogsSummary != "" {
		confidence += 0.1
	}

	if data.RawLogs != "" {
		confidence += 0.2
	}

	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

func (w *LoaderWorker) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return w.Execute(ctx, state)
	})
}

// ========================================================================
// RAG Worker - 向量检索
// ========================================================================

// RAGWorker 执行向量检索
type RAGWorker struct {
	BaseWorker
	retriever domain.VectorRetriever
}

// NewRAGWorker 创建 RAG Worker
func NewRAGWorker(retriever domain.VectorRetriever) *RAGWorker {
	return &RAGWorker{
		BaseWorker: BaseWorker{name: "rag"},
		retriever:  retriever,
	}
}

func (w *RAGWorker) Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	startTime := time.Now()

	fmt.Printf("[RAGWorker] Searching for similar cases (confidence: %.2f)\n", state.Confidence)

	if state.AggregatedData == nil {
		state.RecordWorker(w.Name())
		state.RAGUnavailable = true
		state.ProcessingStatus = domain.StatusProcessing
		return state, nil
	}

	// 构建查询
	queryText := w.buildQuery(state.AggregatedData)

	// 构建错误码列表
	errorCodes := make([]string, 0, len(state.AggregatedData.ErrorCodeStats))
	for code := range state.AggregatedData.ErrorCodeStats {
		errorCodes = append(errorCodes, code)
	}

	// 执行检索
	cases, err := w.retriever.Search(ctx, domain.SearchParams{
		ErrorCodes:    errorCodes,
		EmbeddingText: queryText,
		TopK:          5,
	})
	if err != nil {
		state.RecordWorker(w.Name())
		state.RAGUnavailable = true
		fmt.Printf("[RAGWorker] ⚠️ Search failed: %v (continuing without RAG)\n", err)
		return state, nil
	}

	// 更新状态
	state.RAGCases = cases
	state.RecordWorker(w.Name())

	fmt.Printf("[RAGWorker] ✅ Found %d similar cases\n", len(cases))

	// 记录执行时间
	state.WorkerTimings[w.Name()] = time.Since(startTime)

	return state, nil
}

func (w *RAGWorker) buildQuery(data *domain.AggregatedData) string {
	if data.LogsSummary != "" {
		summary := data.LogsSummary
		if len(summary) > 200 {
			summary = summary[:200]
		}
		return fmt.Sprintf("故障现象：%s", summary)
	}

	if data.RawLogs != "" {
		logs := data.RawLogs
		if len(logs) > 200 {
			logs = logs[:200]
		}
		return fmt.Sprintf("故障现象：%s", logs)
	}

	if len(data.ErrorCodeStats) > 0 {
		codes := make([]string, 0, len(data.ErrorCodeStats))
		for code := range data.ErrorCodeStats {
			codes = append(codes, code)
		}
		return fmt.Sprintf("错误码：%v", codes)
	}

	return "车辆故障诊断"
}

func (w *RAGWorker) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return w.Execute(ctx, state)
	})
}

// ========================================================================
// Reasoning Worker - LLM 推理
// ========================================================================

// ReasoningWorker 执行 LLM 推理
type ReasoningWorker struct {
	BaseWorker
	llm model.ChatModel
}

// NewReasoningWorker 创建 Reasoning Worker
func NewReasoningWorker(llmConfig *llm.GLM4Config) (*ReasoningWorker, error) {
	chatModel, err := llm.NewGLM4ChatModel(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	return &ReasoningWorker{
		BaseWorker: BaseWorker{name: "reasoning"},
		llm:        chatModel,
	}, nil
}

func (w *ReasoningWorker) Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	startTime := time.Now()

	fmt.Printf("[ReasoningWorker] Starting diagnosis (has RAG: %v)\n", len(state.RAGCases) > 0)

	if state.AggregatedData == nil {
		state.RecordWorker(w.Name())
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = "No data to diagnose"
		return state, nil
	}

	// 构建 Prompt
	userPrompt := w.buildUserPrompt(state)

	// 调用 LLM（使用 Eino 标准接口）
	msgs := []*schema.Message{
		{Role: schema.System, Content: w.getSystemPrompt()},
		{Role: schema.User, Content: userPrompt},
	}

	resp, err := w.llm.Generate(ctx, msgs)
	if err != nil {
		state.RecordWorker(w.Name())
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = fmt.Sprintf("LLM call failed: %v", err)
		return state, nil
	}

	// 解析结果
	result, err := w.parseResult(resp.Content)
	if err != nil {
		state.RecordWorker(w.Name())
		state.ProcessingStatus = domain.StatusFailed
		state.ErrorMessage = fmt.Sprintf("Failed to parse result: %v", err)
		return state, nil
	}

	// 更新状态
	state.DiagnosisResult = result
	state.ProcessingStatus = domain.StatusSuccess
	state.Confidence = result.Confidence
	state.RecordWorker(w.Name())

	fmt.Printf("[ReasoningWorker] ✅ Diagnosis completed, confidence: %.2f\n", result.Confidence)

	// 记录执行时间
	state.WorkerTimings[w.Name()] = time.Since(startTime)

	return state, nil
}

func (w *ReasoningWorker) getSystemPrompt() string {
	return `你是一个资深的自动驾驶车辆故障诊断专家，拥有 10 年以上的车企维修经验。

你的职责：
1. 分析车辆故障日志，识别根本原因
2. 参考（但不完全依赖）历史案例
3. 提供可执行的解决方案
4. 评估故障严重程度

输出格式（严格 JSON）：
{
  "root_cause": "根本原因描述",
  "suggestions": ["建议1", "建议2"],
  "severity": "high/medium/low",
  "confidence": 0.85
}`
}

func (w *ReasoningWorker) buildUserPrompt(state *domain.DiagnosisContext) string {
	var sb strings.Builder

	sb.WriteString("## 当前故障数据\n\n")
	sb.WriteString("### 基本信息\n")
	sb.WriteString(fmt.Sprintf("- 批次ID: %s\n", state.BatchID))

	if state.AggregatedData != nil {
		sb.WriteString(fmt.Sprintf("\n### 错误码统计\n%v\n", state.AggregatedData.ErrorCodeStats))

		if state.AggregatedData.LogsSummary != "" {
			sb.WriteString(fmt.Sprintf("\n### 日志摘要\n%s\n", state.AggregatedData.LogsSummary))
		}
	}

	sb.WriteString(w.formatRAGCases(state.RAGCases))

	sb.WriteString("\n请基于以上信息，分析故障根本原因并提供解决方案。")

	return sb.String()
}

func (w *ReasoningWorker) formatRAGCases(cases []domain.SimilarCase) string {
	if len(cases) == 0 {
		return "\n⚠️ 未检索到相似历史案例"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## 相似历史案例 (%d 条)\n", len(cases)))
	for i, c := range cases {
		sb.WriteString(fmt.Sprintf("%d. %s: %s (相似度: %.2f)\n",
			i+1, c.DiagnosisID.String(), c.Summary, c.Similarity))
	}
	return sb.String()
}

func (w *ReasoningWorker) parseResult(content string) (*domain.DiagnosisResult, error) {
	// 简化实现，实际应该解析 JSON
	return &domain.DiagnosisResult{
		RootCause:    "示例根因（需要实现完整解析）",
		Suggestions:  []string{"建议1", "建议2"},
		Severity:     "medium",
		Confidence:   0.75,
		RawLLMOutput: content,
	}, nil
}

func (w *ReasoningWorker) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return w.Execute(ctx, state)
	})
}

// ========================================================================
// Terminal Worker - 终止处理
// ========================================================================

// TerminalWorker 终止流程
type TerminalWorker struct {
	BaseWorker
}

// NewTerminalWorker 创建 Terminal Worker
func NewTerminalWorker() *TerminalWorker {
	return &TerminalWorker{
		BaseWorker: BaseWorker{name: "terminal"},
	}
}

func (w *TerminalWorker) Execute(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	elapsed := time.Since(state.StartTime)

	fmt.Printf("[TerminalWorker] =======================================\n")
	fmt.Printf("[TerminalWorker] Workflow completed\n")
	fmt.Printf("[TerminalWorker] Status: %s\n", state.ProcessingStatus)
	fmt.Printf("[TerminalWorker] Iterations: %d\n", state.CurrentIteration)
	fmt.Printf("[TerminalWorker] Total time: %v\n", elapsed)

	if state.DiagnosisResult != nil {
		fmt.Printf("[TerminalWorker] Root Cause: %s\n", state.DiagnosisResult.RootCause)
		fmt.Printf("[TerminalWorker] Confidence: %.2f\n", state.DiagnosisResult.Confidence)
	}

	for worker, timing := range state.WorkerTimings {
		fmt.Printf("[TerminalWorker]   %s: %v\n", worker, timing)
	}
	fmt.Printf("[TerminalWorker] =======================================\n")

	state.NextStep = compose.END
	return state, nil
}

func (w *TerminalWorker) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, state *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return w.Execute(ctx, state)
	})
}

// ========================================================================
// 辅助函数
// ========================================================================

// GuardResult Guard 检查结果
type GuardResult struct {
	Passed   bool
	RuleName string
	Reason   string
}

// RunGuardsForBatch 为批次执行 Guard 检查
func RunGuardsForBatch(state *domain.DiagnosisContext) GuardResult {
	if state.BatchID == "" {
		return GuardResult{
			Passed:   false,
			RuleName: "batch_id_required",
			Reason:   "BatchID is required",
		}
	}

	return GuardResult{Passed: true}
}

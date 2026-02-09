package nodes

import (
	"context"
	"fmt"
	"regexp"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/gjson"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// LLMNode LLM 诊断 Node
//
// 职责：
// 1. 生成 Prompt（带思维链）
// 2. 调用 GLM-4（使用 Eino Model 接口）
// 3. 解析 JSON 结果
// 4. 降级处理
type LLMNode struct {
	chatModel model.ChatModel
}

// NewLLMNode 创建 LLM Node
func NewLLMNode(chatModel model.ChatModel) *LLMNode {
	return &LLMNode{
		chatModel: chatModel,
	}
}

// Transform 实现 Eino Node 接口
func (n *LLMNode) Transform(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
	// 1. 构造诊断输入
	diagnosisInput := &llm.DiagnosisInput{
		BatchID:         input.BatchID,
		ErrorCodes:      formatErrorCodes(input.AggregatedData),
		LogsSummary:     input.AggregatedData.LogsSummary,
		CasesText:       formatRAGCases(input.RAGCases, input.RAGUnavailable),
		RAGUnavailable:  input.RAGUnavailable,
	}

	// 2. 生成 Prompt（带思维链）
	prompt := llm.GeneratePrompt(diagnosisInput)

	// 3. 调用 LLM（使用 Eino Model 接口）
	// 📌 P0 改进：使用 Eino Model 接口，自动获得 Token 统计、链路追踪、重试熔断
	messages := []*schema.Message{
		{
			Role:    schema.System,
			Content: "你是一个资深的自动驾驶车辆故障诊断专家。",
		},
		{
			Role:    schema.User,
			Content: prompt,
		},
	}

	resp, err := n.chatModel.Generate(ctx, messages)
	if err != nil {
		input.ProcessingStatus = domain.StatusFailed
		input.ErrorMessage = fmt.Sprintf("LLM generation failed: %v", err)
		return input, err
	}

	// 4. 解析 JSON 结果（用正则提取 JSON）
	rawJSON := cleanJSON(resp.Content)

	// 5. 解析诊断结果
	result := &domain.DiagnosisResult{
		RawLLMOutput: rawJSON,
	}

	// 使用 gjson 提取字段
	result.RootCause = gjson.Get(rawJSON, "root_cause").String()
	result.Suggestions = parseSuggestions(gjson.Get(rawJSON, "suggestions"))
	result.Severity = gjson.Get(rawJSON, "severity").String()
	result.Confidence = gjson.Get(rawJSON, "confidence").Float()

	// 6. P1-2: 验证 LLM 输出有效性
	if !isValidDiagnosisResult(result) {
		input.ProcessingStatus = domain.StatusFailed
		input.ErrorMessage = fmt.Sprintf("LLM output validation failed: root_cause=%s, severity=%s, confidence=%.2f",
			result.RootCause, result.Severity, result.Confidence)
		return input, fmt.Errorf("invalid LLM output: %s", input.ErrorMessage)
	}

	// 7. 更新状态
	input.DiagnosisResult = result
	input.ProcessingStatus = domain.StatusSuccess

	return input, nil
}

// formatErrorCodes 格式化错误码
func formatErrorCodes(data *domain.AggregatedData) string {
	if data == nil || data.ErrorCodeStats == nil {
		return "无"
	}

	codes := make([]string, 0)
	for code := range data.ErrorCodeStats {
		codes = append(codes, code)
	}

	return fmt.Sprintf("%v", codes)
}

// formatRAGCases 格式化 RAG 检索结果
func formatRAGCases(cases []domain.SimilarCase, unavailable bool) string {
	if unavailable {
		return "⚠️ 知识库当前不可用（数据库连接失败），请仅根据通用知识诊断，并降低 confidence 到 0.5 以下。"
	}

	if len(cases) == 0 {
		return "未检索到相似案例。"
	}

	result := fmt.Sprintf("检索到 %d 条相似案例：\n", len(cases))
	for i, c := range cases {
		result += fmt.Sprintf("%d. %s\n", i+1, c.Summary)
	}

	return result
}

// cleanJSON 清理 JSON（📌 P2 改进：用正则而不是字符串切割）
//
// 从 LLM 输出中提取 JSON，处理 Markdown 代码块、clean_json 标记等
func cleanJSON(raw string) string {
	// 1. 使用正则提取 JSON 代码块
	// 📌 P2 改进：更稳健的正则，支持 ```json 和 ```
	jsonBlockRegex := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

	match := jsonBlockRegex.FindStringSubmatch(raw)
	if len(match) > 1 {
		return match[1]
	}

	// 2. 如果没有代码块，直接返回
	return raw
}

// isValidDiagnosisResult P1-2: 验证 LLM 输出有效性
func isValidDiagnosisResult(result *domain.DiagnosisResult) bool {
	// RootCause 不能为空
	if result.RootCause == "" {
		return false
	}

	// Severity 必须是有效值
	validSeverities := map[string]bool{
		"critical": true,
		"high":     true,
		"medium":   true,
		"low":      true,
		"info":     true,
	}
	if !validSeverities[result.Severity] {
		return false
	}

	// Confidence 必须在 [0, 1] 范围内
	if result.Confidence < 0 || result.Confidence > 1 {
		return false
	}

	return true
}

// parseSuggestions 解析建议列表
func parseSuggestions(result gjson.Result) []string {
	if !result.IsArray() {
		return []string{}
	}

	suggestions := []string{}
	result.ForEach(func(_, v gjson.Result) bool {
		suggestions = append(suggestions, v.String())
		return true
	})

	return suggestions
}

// GraphNode 返回 Eino Chain 可用的 Lambda Node
func (n *LLMNode) GraphNode() *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, input *domain.DiagnosisContext) (*domain.DiagnosisContext, error) {
		return n.Transform(ctx, input)
	})
}

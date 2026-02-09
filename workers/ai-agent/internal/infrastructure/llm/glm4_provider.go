package llm

import (
	"context"
	"fmt"
	"os"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino-ext/components/model/openai"
)

// GLM4Config GLM-4 配置
type GLM4Config struct {
	APIKey string
	BaseURL string
	Model  string
}

// NewGLM4ChatModel 创建 GLM-4 Chat Model（使用 Eino Model 接口）
//
// 📌 P0 改进：使用 Eino 的 Model 接口，而不是裸 HTTP 调用
// - 自动获得 Token 统计（成本控制）
// - 自动获得链路追踪（性能监控）
// - 自动获得统一重试和熔断（高可用）
func NewGLM4ChatModel(conf *GLM4Config) (model.ChatModel, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if conf.APIKey == "" {
		conf.APIKey = os.Getenv("GLM_API_KEY")
	}

	if conf.Model == "" {
		conf.Model = "glm-4.7-flash" // 默认使用快版本（成本低、速度快）
	}

	// 设置默认 Base URL
	if conf.BaseURL == "" {
		conf.BaseURL = "https://open.bigmodel.cn/api/paas/v4/"
	}

	// 📌 P0 改进：使用 Eino 的 ChatModel 接口（而不是裸 HTTP）
	// - 自动获得 Token 统计（成本控制）
	// - 自动获得链路追踪（性能监控）
	// - 自动获得统一重试和熔断（高可用）
	//
	// GLM-4 兼容 OpenAI API，使用 eino-ext 的 OpenAI 适配器
	maxTokens := 4000
	temperature := float32(0.7)
	topP := float32(0.9)

	config := &openai.ChatModelConfig{
		APIKey:       conf.APIKey,
		BaseURL:      conf.BaseURL,
		Model:        conf.Model,
		MaxTokens:    &maxTokens,
		Temperature:  &temperature,
		TopP:         &topP,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}, // P1: 显式开启 JSON Mode
	}

	chatModel, err := openai.NewChatModel(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create GLM-4 chat model: %w", err)
	}

	return chatModel, nil
}

// GeneratePrompt 生成诊断 Prompt（带思维链）
func GeneratePrompt(ctx *DiagnosisInput) string {
	// 📌 P1 改进：思维链（CoT）+ 动态降级标记
	prompt := fmt.Sprintf(`# 角色
你是一个资深的自动驾驶车辆故障诊断专家，擅长分析日志和错误码。

# 任务
基于提供的故障数据，分析根本原因并给出解决建议。

# 当前故障数据
- 批次ID: %s
- 错误码: %s
- 日志摘要:
%s

## 相似历史案例
%s

## 诊断要求
1. 请先在 <analysis> 标签中进行分析推理
2. 然后输出 JSON 格式的诊断结果

## 输出格式

### 分析过程
<analysis>
1. 错误码分布分析
2. 日志时间轴规律
3. 历史案例相似性
4. 根本原因推断
</analysis>

### 诊断结果
请严格按照以下 JSON 格式输出（不要包含 Markdown 代码块标记）：
{
  "root_cause": "根本原因描述",
  "suggestions": ["建议1", "建议2", "建议3"],
  "severity": "high/medium/low",
  "confidence": 0.85
}
`,
		ctx.BatchID,
		ctx.ErrorCodes,
		ctx.LogsSummary,
		ctx.CasesText,
	)

	return prompt
}

// DiagnosisInput 诊断输入（用于 Prompt 生成）
type DiagnosisInput struct {
	BatchID      string
	ErrorCodes   string
	LogsSummary  string
	CasesText    string
	RAGUnavailable bool
}

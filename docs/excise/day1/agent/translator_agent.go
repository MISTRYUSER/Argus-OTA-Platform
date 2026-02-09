package agent

import (
	"context"
	"log"
	"os"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino-ext/components/model/openai"
)

  type TranslatorToolInput struct {
        InputString string `json:"word" jsonschema:"required,description=英文句子"`
  }

  type TranslatorToolOutput struct {
        CNWord string `json:"CNWord" description:"翻译后的中文原句"`
  }

 // 修改 agent/translator_agent.go 中的这个函数
 func TranslatorToolFunc(ctx context.Context, input *TranslatorToolInput) (*TranslatorToolOutput, error) {
    log.Printf("🛠️  【Go代码被执行了】正在处理: %s", input.InputString)
    
    // 强制加个前缀，这样如果最终回复里有这个前缀，就证明工具真的跑了
    return &TranslatorToolOutput{
        CNWord: "【🤖 经过了Go工具处理】" + input.InputString, 
    }, nil
}
  func CreateTranslatorTool() tool.InvokableTool {
	translatorTool, err :=  utils.InferTool(
			"translator",
			"将英文翻译成中文",
			TranslatorToolFunc,
		)
		if err != nil {
			log.Fatalf("创建翻译工具失败：%v", err)
		}
		return translatorTool
  }

func NewTranslatorAgent() adk.Agent{
	ctx := context.Background()
	llmModel, err := createOpenaiModel(ctx)
	if err != nil {
        log.Fatalf("创建LLM模型失败：%v", err)
    }

	TranslatorTool := CreateTranslatorTool()

	agentConfig := &adk.ChatModelAgentConfig{
		Name:        "TranslatorAgent", // Agent名称（唯一）
		Description: "翻译助手，可以将英文翻译成中文", // 功能描述
		// 系统提示词（核心！告诉Agent怎么工作）
		Instruction: `你是一个只会调用工具的笨拙机器人。
你的唯一任务是：收到用户的英文后，**必须**调用 translator 工具进行处理。
**绝对不要**自己翻译，直接把工具返回的结果告诉用户。`,
		Model:       llmModel, // 绑定LLM模型
		// 绑定工具（ToolsNodeConfig包装工具列表）
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{TranslatorTool}, // 放入翻译工具
			},
		},
		MaxIterations: 5, // 最大迭代次数（防止无限循环）
	}
	translatorAgent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		log.Fatalf("创建翻译Agent失败：%v", err)
	}
	return translatorAgent
}
func createOpenaiModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	apiKey := os.Getenv("ZHIPU_API_KEY")
	if apiKey == "" {
		log.Fatal("请设置环境变量 ZHIPU_API_KEY")
	}

	modelConfig := &openai.ChatModelConfig{
		APIKey:  apiKey,
		Model:   "glm-4-flash", // 智谱 AI 模型（免费版本）
		BaseURL: "https://open.bigmodel.cn/api/paas/v4/", // 智谱 AI 的 API endpoint
	}

	return openai.NewChatModel(ctx, modelConfig)
}
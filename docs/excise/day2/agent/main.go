//go:build ignore

// 包名：固定为 agent（根据项目实际目录调整，项目中通常统一用 agent 包）
package agent

import (
        "context"
        "fmt"
        "log"
        "os"

        "github.com/cloudwego/eino/adk"
        "github.com/cloudwego/eino/components/model"
        "github.com/cloudwego/eino/components/tool"
        "github.com/cloudwego/eino/compose"
        "github.com/cloudwego/eino/schema"
        "github.com/cloudwego/eino-ext/components/model/openai"

        // 导入项目依赖（根据实际情况调整）
        "your-project-name/chat"   // 项目中封装的 LLM 模型（如 OpenAI 配置）
        "your-project-name/tool2"  // 项目中自定义的 Eino 工具（如需绑定工具）
)

// ======================================
// 可选：绑定工具（不需要工具则注释/删除这部分）
// 说明：如果 Agent 需要调用工具，在此定义工具创建逻辑；无需工具则跳过
// ======================================
// 步骤1：导入/定义需要的工具（示例：绑定 PDF 解析工具 + 简历评分工具）
func getAgentTools() []tool.BaseTool {
        var tools []tool.BaseTool

        // 添加工具1：PDF 解析工具（项目中已实现的工具）
        tools = append(tools, tool2.CreatePDFToTextTool())

        // 添加工具2：简历评分工具（根据需要添加，不需要则注释）
        // tools = append(tools, tool2.CreateResumeScoreTool())

        // 可继续添加更多工具...
        return tools
}

// ======================================
// 第一步：创建 LLM 模型（两种方式二选一，项目中常用方式1）
// 说明：Agent 依赖 LLM 处理逻辑，必须绑定模型
// ======================================
// 方式1：使用项目封装的模型（推荐，统一配置管理）

// 方式2：直接创建 OpenAI 模型（备用，适合快速测试）
func createOpenAIModelDirectly(ctx context.Context) (model.ToolCallingChatModel, error) {
        // 从环境变量获取 API Key（避免硬编码）
        apiKey := os.Getenv("OPENAI_API_KEY")
        if apiKey == "" {
                return nil, fmt.Errorf("请设置环境变量 OPENAI_API_KEY")
        }

        // 配置 OpenAI 模型
        modelConfig := &openai.ChatModelConfig{
                APIKey: apiKey,
                Model:  "glm-4-flash", // 模型名称（可替换为 gpt-4 等）
				BaseURL: "https://open.bigmodel.cn/api/paas/v4/",
                // 可选配置：设置超时时间、温度值等
                // Timeout: 30 * time.Second,
                // Temperature: 0.7,
        }

        // 创建模型实例
        return openai.NewChatModel(ctx, modelConfig)
}

// ======================================
// 第二步：核心函数：创建单 Agent（必须修改占位符！）
// 函数命名规范：New[Agent功能]Agent（如 NewResumeAnalysisAgent）
// ======================================
func NewParseAgent() adk.Agent {
        ctx := context.Background()

        // 1. 初始化 LLM 模型（二选一，项目中用方式1）
        llmModel := createLLMModel(ctx)
        // llmModel, err := createOpenAIModelDirectly(ctx)
        // if err != nil {
        //         log.Fatalf("LLM 模型初始化失败：%v", err)
        // }

        // 2. 初始化工具（不需要工具则设为 nil）
        var toolsConfig adk.ToolsConfig
        agentTools := getAgentTools()
        if len(agentTools) > 0 {
                // 用 ToolsNode 包装工具列表（Agent 识别的工具包格式）
                toolsNode, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
                        Tools: agentTools,
                })
                if err != nil {
                        log.Fatalf("[%s] 工具包创建失败：%v", "[Agent名称]", err)
                }
                toolsConfig = adk.ToolsConfig{
                        ToolsNodeConfig: toolsNode.Config(), // 绑定工具包到 Agent 配置
                }
        }

        // 3. 配置 Agent 核心参数（重点修改这部分！）
        agentConfig := &adk.ChatModelAgentConfig{
                // 占位符1：Agent 唯一名称（必须唯一，多 Agent 协作时用）
                Name: "[Agent名称]", // 示例："ResumeAnalysisAgent"
                // 占位符2：Agent 功能描述（简洁说明能干嘛，多 Agent 时方便其他 Agent 识别）
                Description: "[Agent功能描述]", // 示例："解析PDF简历，生成结构化分析报告和0-100分评分"
                // 占位符3：系统提示词（核心！告诉 Agent 角色、工作流程、输出格式）
                Instruction: `[系统提示词，详细说明 Agent 的工作规则]
示例（简历分析 Agent）：
你是一名资深简历分析专家，遵循以下工作流程：
1. 若用户提供 PDF 简历路径，先调用 "pdf_to_text" 工具提取文本；
2. 若用户已提供简历文本，直接进行分析；
3. 从「模块完整度、技能匹配度、量化成果、语言表达」4个维度分析；
4. 给出 0-100 分评分和可执行的改进建议；
5. 输出格式：分点列出分析结果、评分、改进建议，语言简洁专业。`,
                // 绑定 LLM 模型
                Model: llmModel,
                // 绑定工具（不需要工具则设为 adk.ToolsConfig{}）
                ToolsConfig: toolsConfig,
                // 最大迭代次数（防止 Agent 无限循环调用工具，默认20，建议设 5-10）
                MaxIterations: 8,
                // 可选配置：是否启用流式输出（默认 false，一次性返回结果）
                // EnableStreaming: true,
        }

        // 4. 创建 ChatModelAgent 实例（框架核心方法，无需修改）
        agentInstance, err := adk.NewChatModelAgent(ctx, agentConfig)
        if err != nil {
                log.Fatalf("[%s] Agent 创建失败：%v", agentConfig.Name, err)
        }

        // 日志提示（可选，方便调试）
        log.Printf("✅ [%s] Agent 初始化完成（支持工具数：%d）", agentConfig.Name, len(agentTools))
        return agentInstance
}

// ======================================
// 第三步：测试函数（可选，单独调试 Agent 用）
// 说明：不用集成到主程序，单独运行这个文件即可测试 Agent 功能
// ======================================
func Test[Agent功能]Agent() {
        ctx := context.Background()

        // 1. 创建 Agent 实例
        testAgent := New[Agent功能]Agent()

        // 2. 构建测试输入（模拟用户需求，根据 Agent 功能修改）
        testInput := &adk.AgentInput{
                Messages: []adk.Message{
                        // 占位符4：模拟用户输入（根据 Agent 功能调整）
                        schema.UserMessage("[用户测试输入]"), // 示例："请分析我这份简历，PDF路径：/Users/xxx/简历.pdf"
                },
                // EnableStreaming: true, // 如需流式输出，设为 true
        }

        // 3. 运行 Agent（核心：获取事件迭代器）
        eventIter := testAgent.Run(ctx, testInput)

        // 4. 循环处理 Agent 输出事件（固定逻辑，无需修改）
        log.Printf("\n🚀 开始运行 [%s]，输入：%s", testAgent.Name(ctx), testInput.Messages[0].Content())
        for {
                event, ok := eventIter.Next()
                if !ok {
                        log.Println("\n🔚 Agent 运行结束")
                        break
                }

                // 处理错误
                if event.Err != nil {
                        log.Fatalf("\n❌ Agent 运行出错：%v", event.Err)
                }

                // 处理控制行为（如跳转其他 Agent，单 Agent 通常用不到）
                if event.Action != nil {
                        log.Printf("\n📋 Agent 控制行为：%+v", event.Action)
                        continue
                }

                // 处理输出结果（区分助手消息和工具消息）
                if event.Output != nil && event.Output.MessageOutput != nil {
                        msgOutput := event.Output.MessageOutput
                        switch msgOutput.Role {
                        case schema.Assistant:
                                log.Printf("\n📢 Agent 回复：\n%s", msgOutput.Message.Content())
                        case schema.Tool:
                                log.Printf("\n🛠️  工具调用结果（工具：%s）：\n%s", msgOutput.ToolName, msgOutput.Message.Content())
                        case schema.User:
                                log.Printf("\n👤 用户输入：%s", msgOutput.Message.Content())
                        }
                }
        }
}

// ======================================
// 测试入口（单独运行时执行）
// 命令：go run agent/[agent文件名].go
// ======================================
func main() {
        // 调用测试函数
        Test[Agent功能]Agent()
}
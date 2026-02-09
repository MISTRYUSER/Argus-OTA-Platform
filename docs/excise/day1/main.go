package main

import (
	"context"
	"log"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/xuewentao/argus-ota-platform/docs/excise/day1/agent"
)

func main() {
    ctx := context.Background()

    // 1. 创建翻译Agent
    translatorAgent := agent.NewTranslatorAgent()
    log.Printf("✅ Agent创建成功：名称=%s，描述=%s",
        translatorAgent.Name(ctx),
        translatorAgent.Description(ctx),
    )

    // 2. 构建输入（用户查询）
    agentInput := &adk.AgentInput{
        Messages: []adk.Message{
            schema.UserMessage(`And as I sat there brooding on the old, unknown world, I thought of Gatsbys wonder when he first picked out the green light at the end of Daisy's dock. He had come a long way to this blue lawn, and his dream must have seemed so close that he could hardly fail to grasp it. He did not know that it was already behind him, somewhere back in that vast obscurity beyond the city, where the dark fields of the republic rolled on under the night.

Gatsby believed in the green light, the orgastic future that year by year recedes before us. It eluded us then, but that's no matter—to-morrow we will run faster, stretch out our arms farther. . . . And one fine morning——

So we beat on, boats against the current, borne back ceaselessly into the past.`), // 用户消息
        },
    }

    // 3. 运行Agent（返回迭代器，用于接收事件）
    eventIter := translatorAgent.Run(ctx, agentInput)

    // 4. 循环处理AgentEvent（核心！读取所有输出事件）
    for {
        event, ok := eventIter.Next()
        if !ok {
            log.Println("🔚 Agent运行结束")
            break // 迭代器关闭，运行结束
        }

        // 处理错误
        if event.Err != nil {
            log.Fatalf("❌ Agent运行出错：%v", event.Err)
        }

        // 处理输出结果（MessageOutput）
        if event.Output != nil && event.Output.MessageOutput != nil {
            msgOutput := event.Output.MessageOutput
            // 区分消息类型：助手消息（Agent回复）、工具消息（工具调用结果）
            switch msgOutput.Role {
            case schema.Assistant:
                log.Printf("\n📢 Agent[%s] 回复：%s", event.AgentName, msgOutput.Message.Content)
            case schema.Tool:
                log.Printf("\n🛠️  工具[%s] 结果：%s", msgOutput.ToolName, msgOutput.Message.Content)
            }
        }
    }
}
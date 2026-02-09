package kafka

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
)

// Consumer Kafka 消费者
type Consumer struct {
	reader  *kafka.Reader
	service *application.MultiAgentService
}

// NewConsumer 创建 Kafka 消费者
func NewConsumer(reader *kafka.Reader, service *application.MultiAgentService) *Consumer {
	return &Consumer{
		reader:  reader,
		service: service,
	}
}

// Consume 消费 Kafka 消息（阻塞运行）
func (c *Consumer) Consume(ctx context.Context) error {
	for {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			log.Println("[Kafka] Consumer stopped")
			return ctx.Err()
		default:
		}

		// 读取消息
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("[Kafka] Failed to read message: %v", err)
			continue
		}

		// 解析事件
		var event GatheringCompletedEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("[Kafka] Failed to unmarshal event: %v", err)
			continue
		}

		log.Printf("[Kafka] Received event: type=%s, batchID=%s", event.EventType, event.BatchID)

		// 调用 MultiAgentService 进行诊断
		if err := c.service.DiagnoseBatch(ctx, event.BatchID); err != nil {
			log.Printf("[Kafka] Failed to diagnose batch %s: %v", event.BatchID, err)
			// 可以选择重试或记录到死信队列
			continue
		}

		log.Printf("[Kafka] Successfully diagnosed batch %s", event.BatchID)
	}
}

// Close 关闭消费者
func (c *Consumer) Close() error {
	return c.reader.Close()
}

// GatheringCompletedEvent 聚合完成事件
type GatheringCompletedEvent struct {
	EventType string         `json:"event_type"`
	BatchID   string         `json:"batch_id"`
	Timestamp string         `json:"timestamp"`
	Data      AggregatedData `json:"data"`
}

// AggregatedData 聚合数据（来自 Python Worker）
type AggregatedData struct {
	BatchID    string                 `json:"batch_id"` // 改为 string，因为前面是 string
	TotalFiles int                    `json:"total_files"`
	TotalLogs  int64                   `json:"total_logs"`
	ErrorCodes map[string]int         `json:"error_codes"`
	ChartFiles []string               `json:"chart_files"`
	Statistics map[string]interface{} `json:"statistics"`
}

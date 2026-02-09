package kafka

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"

	"github.com/google/uuid"
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

		// 读取消息（手动提交 offset）
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("[Kafka] Failed to read message: %v", err)
			continue
		}

		// 解析事件
		var event GatheringCompletedEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("[Kafka] Failed to unmarshal event: %v", err)
			// 脏消息直接提交，避免 poison pill 无限重试
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				log.Printf("[Kafka] Failed to commit malformed message: %v", commitErr)
			}
			continue
		}
		if event.BatchID == "" {
			log.Printf("[Kafka] Invalid event: empty batch_id")
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				log.Printf("[Kafka] Failed to commit invalid message: %v", commitErr)
			}
			continue
		}
		if _, err := uuid.Parse(event.BatchID); err != nil {
			log.Printf("[Kafka] Invalid event: bad batch_id=%s err=%v", event.BatchID, err)
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				log.Printf("[Kafka] Failed to commit invalid message: %v", commitErr)
			}
			continue
		}
		if !isGatheringCompletedEvent(event.EventType) {
			log.Printf("[Kafka] Ignored event type=%s", event.EventType)
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				log.Printf("[Kafka] Failed to commit ignored message: %v", commitErr)
			}
			continue
		}

		log.Printf("[Kafka] Received event: type=%s, batchID=%s", event.EventType, event.BatchID)
		topErrorCodes := extractTopKErrorCodes(event.Data.ErrorCodes, 5)

		// 调用 MultiAgentService 进行诊断
		if err := c.service.DiagnoseBatchWithTopErrorCodes(ctx, event.BatchID, topErrorCodes); err != nil {
			log.Printf("[Kafka] Failed to diagnose batch %s: %v", event.BatchID, err)
			// 业务失败不提交，让 Kafka 重试
			continue
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("[Kafka] Failed to commit message for batch %s: %v", event.BatchID, err)
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
	TotalLogs  int64                  `json:"total_logs"`
	ErrorCodes map[string]int         `json:"error_codes"`
	ChartFiles []string               `json:"chart_files"`
	Statistics map[string]interface{} `json:"statistics"`
}

func isGatheringCompletedEvent(eventType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(eventType))
	return normalized == "gatheringcompleted" || normalized == "gathering-completed"
}

func extractTopKErrorCodes(errorCodeStats map[string]int, k int) []string {
	if len(errorCodeStats) == 0 || k <= 0 {
		return []string{}
	}

	type codeCount struct {
		code  string
		count int
	}
	counts := make([]codeCount, 0, len(errorCodeStats))
	for code, count := range errorCodeStats {
		if strings.TrimSpace(code) == "" {
			continue
		}
		counts = append(counts, codeCount{code: code, count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			return counts[i].code < counts[j].code
		}
		return counts[i].count > counts[j].count
	})
	if len(counts) < k {
		k = len(counts)
	}

	result := make([]string, 0, k)
	for i := 0; i < k; i++ {
		result = append(result, counts[i].code)
	}
	return result
}

package kafka

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

type Consumer struct {
	reader           *kafka.Reader
	graph          *application.DiagnosisGraph
	completionWriter *kafka.Writer
	completionTopic  string
}

func NewConsumer(reader *kafka.Reader, graph *application.DiagnosisGraph, completionWriter *kafka.Writer, completionTopic string) *Consumer {
	return &Consumer{
		reader:           reader,
		graph:            graph,
		completionWriter: completionWriter,
		completionTopic:  completionTopic,
	}
}

func (c *Consumer) Consume(ctx context.Context) error {
	for {
		// 检查 context 有没有取消
		select {
		case <-ctx.Done():
			log.Println("[Kafka] Consumer stopped")
			return ctx.Err()
		default:
		}

		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("[Kafka] Failed to read message: %v", err)
			continue
		}

		var event GatheringCompletedEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("[Kafka] Failed to unmarshal event: %v", err)
			// P1-2: 即使 unmarshal 失败也不 commit，让 Kafka 重试
			continue
		}

		log.Printf("[Kafka] Received event: type=%s, batchID=%s", event.EventType, event.BatchID)

		// 调用 DiagnosisGraph 进行诊断
		result, err := c.graph.Run(ctx, event.BatchID)
		if err != nil {
			log.Printf("[Kafka] Failed to diagnose batch %s: %v", event.BatchID, err)
			// P1-2: 诊断失败不 commit，允许 Kafka 重试
			continue
		}

		// 记录诊断结果
		log.Printf("[Diagnosis] RootCause: %s, Severity: %s, Confidence: %.2f",
			result.RootCause, result.Severity, result.Confidence)

		if err := c.publishDiagnosisCompleted(ctx, event.BatchID, result); err != nil {
			log.Printf("[Kafka] Failed to publish DiagnosisCompleted for batch %s: %v", event.BatchID, err)
			// P1-2: 发布失败不 commit，允许 Kafka 重试
			continue
		}

		// P1-2: 只在全部成功后才 commit 消息
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("[Kafka] Failed to commit message for batch %s: %v", event.BatchID, err)
			// commit 失败也可能导致重复处理，但幂等性可以处理
			continue
		}

		log.Printf("[Kafka] Successfully diagnosed and committed batch %s", event.BatchID)
	}
}

// Close 关闭消费者
func (c *Consumer) Close() error {
	if c.completionWriter != nil {
		_ = c.completionWriter.Close()
	}
	return c.reader.Close()
}

// GatheringCompletedEvent 聚合完成事件
//
// 由 Python Worker 发布，表示数据聚合完成，可以进行 AI 诊断
type GatheringCompletedEvent struct {
	EventType string    `json:"event_type"` // "gathering-completed"
	BatchID   string    `json:"batch_id"`
	Timestamp time.Time `json:"timestamp"`
}

type DiagnosisCompletedEvent struct {
	Version          string                       `json:"version"`
	EventType        string                       `json:"event_type"`
	BatchID          string                       `json:"batch_id"`
	DiagnosisID      string                       `json:"diagnosis_id"` // P0: 缺失字段，已补充
	DiagnosisSummary string                       `json:"diagnosis_summary"`
	TopErrorCodes    []domain.ErrorCodeSummary    `json:"top_error_codes,omitempty"`
	RootCause        string                       `json:"root_cause"`
	Severity         string                       `json:"severity"`
	Confidence       float64                      `json:"confidence"`
	Suggestions      []string                     `json:"suggestions,omitempty"`
	Status           string                       `json:"status"`
	OccurredAt       time.Time                    `json:"occurred_at"`
}

func (c *Consumer) publishDiagnosisCompleted(ctx context.Context, batchID string, result *domain.DiagnosisResult) error {
	if c.completionWriter == nil || c.completionTopic == "" {
		return nil
	}

	// 生成新的 DiagnosisID
	diagnosisID := uuid.New().String()

	// 构建 ErrorCodeSummary (从 AggregatedData 转换)
	topErrorCodes := make([]domain.ErrorCodeSummary, 0)
	// TODO: 从 result 中提取 Top-K 错误码

	event := DiagnosisCompletedEvent{
		Version:          "v1.0",
		EventType:        "DiagnosisCompleted",
		BatchID:          batchID,
		DiagnosisID:      diagnosisID,
		DiagnosisSummary: result.RootCause, // 使用 RootCause 作为摘要
		TopErrorCodes:    topErrorCodes,
		RootCause:        result.RootCause,
		Severity:         result.Severity,
		Confidence:       result.Confidence,
		Suggestions:      result.Suggestions,
		Status:           "completed",
		OccurredAt:       time.Now(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return c.completionWriter.WriteMessages(ctx, kafka.Message{
		Key:   []byte(batchID),
		Value: data,
		Time:  time.Now(),
		Topic: c.completionTopic,
	})
}

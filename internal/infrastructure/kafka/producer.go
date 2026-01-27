package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/IBM/sarama"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

const maxMessageSize = 1024 * 1024 // 1MB

// kafkaEventProducer - Kafka 事件发布器实现（小写，包私有）
type kafkaEventProducer struct {
	producer 	sarama.SyncProducer
	dlqProducer sarama.SyncProducer
	topic    	string
	dlqTopic    string
}

// NewKafkaEventProducer - 创建 Kafka Producer
// 返回接口类型,而不是具体实现
func NewKafkaEventProducer(brokers []string, topic string, dlqTopic string) (messaging.KafkaEventPublisher, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true

	config.Producer.Idempotent = true
	config.Producer.Retry.Backoff = 100 * time.Millisecond

	producer, err := sarama.NewSyncProducer(brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka producer: %w", err)
	}

	// 创建 DLQ Producer (可选)
	var dlqProducer sarama.SyncProducer
	if dlqTopic != "" {
		dlqProducer, err = sarama.NewSyncProducer(brokers, config)
		if err != nil {
			log.Printf("[Kafka] Failed to create DLQ producer: %v", err)
			// DLQ 失败不影响主 Producer
		}
	}

	log.Printf("[Kafka] Producer created successfully. Brokers: %v, Topic: %s, DLQ Topic: %s", brokers, topic, dlqTopic)

	return &kafkaEventProducer{
		producer:   producer,
		dlqProducer: dlqProducer,
		topic:      topic,
		dlqTopic:   dlqTopic,
	}, nil
}

// PublishEvents - 批量发布领域事件（实现 messaging.KafkaEventPublisher 接口）
func (k *kafkaEventProducer) PublishEvents(ctx context.Context, events []domain.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}

	log.Printf("[Kafka] Publishing %d events to topic: %s", len(events), k.topic)

	for i, event := range events {
		switch e := event.(type) {
		case domain.BatchCreated:
			if err := k.publishBatchCreated(ctx, e); err != nil {
				return fmt.Errorf("failed to publish event %d: %w", i, err)
			}
		case domain.BatchStatusChanged:
			if err := k.publishStatusChanged(ctx, e); err != nil {
				return fmt.Errorf("failed to publish event %d: %w", i, err)
			}
		case domain.FileParsed:
			if err := k.publishFileParsed(ctx, e); err != nil {
				return fmt.Errorf("failed to publish event %d: %w", i, err)
			}
		case domain.GatheringCompleted:
			if err := k.publishGatheringCompleted(ctx, e); err != nil {
				return fmt.Errorf("failed to publish event %d: %w", i, err)
			}
		case domain.DiagnosisCompleted:
			if err := k.publishDiagnosisCompleted(ctx, e); err != nil {
				return fmt.Errorf("failed to publish event %d: %w", i, err)
			}
		default:
			log.Printf("[Kafka] Unknown event type: %T", event)
		}
	}

	log.Printf("[Kafka] Successfully published %d events", len(events))
	return nil
}
func (k *kafkaEventProducer) PublishEventsWithRetry(ctx context.Context, events []domain.DomainEvent) error {
	for _, event := range events {
		if err := k.publishWithRetry(ctx, event, 0); err != nil {
			// 发送到 DLQ (死信队列)
			k.sendToDLQ(ctx, event, err)
			return fmt.Errorf("failed to publish event after retries: %w", err)
		}
	}
	return nil
}
// 单个事件发布（带重试逻辑）
func (k *kafkaEventProducer) publishWithRetry(ctx context.Context, event domain.DomainEvent, attempt int) error {
	if attempt >= 5 {
		return fmt.Errorf("max retries exceeded")
	}

	// 将事件转换为 Kafka 消息
	var kafkaMsg *sarama.ProducerMessage
	switch e := event.(type) {
	case domain.BatchCreated:
		kafkaMsg = &sarama.ProducerMessage{
			Topic: k.topic,
			Key:   sarama.StringEncoder(e.BatchID.String()),
			Value: sarama.StringEncoder(fmt.Sprintf(`{"event_type":"BatchCreated","batch_id":"%s","vehicle_id":"%s","vin":"%s","timestamp":"%s"}`,
				e.BatchID, e.VehicleID, e.VIN, e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"))),
		}
	case domain.BatchStatusChanged:
		kafkaMsg = &sarama.ProducerMessage{
			Topic: k.topic,
			Key:   sarama.StringEncoder(e.BatchID.String()),
			Value: sarama.StringEncoder(fmt.Sprintf(`{"event_type":"StatusChanged","batch_id":"%s","old_status":"%s","new_status":"%s","timestamp":"%s"}`,
				e.BatchID, e.OldStatus.String(), e.NewStatus.String(), e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"))),
		}
	case domain.FileParsed:
		kafkaMsg = &sarama.ProducerMessage{
			Topic: k.topic,
			Key:   sarama.StringEncoder(e.BatchID.String()),
			Value: sarama.StringEncoder(fmt.Sprintf(`{"event_type":"FileParsed","batch_id":"%s","file_id":"%s","timestamp":"%s"}`,
				e.BatchID, e.FileID, e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"))),
		}
	case domain.GatheringCompleted:
		data, _ := json.Marshal(map[string]interface{}{
			"event_type":  "GatheringCompleted",
			"version":     e.Version,
			"batch_id":    e.BatchID.String(),
			"total_files": e.TotalFiles,
			"chart_files": e.ChartFiles,
			"timestamp":   e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
		})
		kafkaMsg = &sarama.ProducerMessage{
			Topic: k.topic,
			Key:   sarama.StringEncoder(e.BatchID.String()),
			Value: sarama.ByteEncoder(data),
		}
	case domain.DiagnosisCompleted:
		data, _ := json.Marshal(map[string]interface{}{
			"event_type":        "DiagnosisCompleted",
			"version":           e.Version,
			"batch_id":          e.BatchID.String(),
			"diagnosis_id":      e.DiagnosisID.String(),
			"diagnosis_summary": e.DiagnosisSummary,
			"top_error_codes":   e.TopErrorCodes,
			"token_usage":       e.TokenUsage,
			"timestamp":         e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
		})
		kafkaMsg = &sarama.ProducerMessage{
			Topic: k.topic,
			Key:   sarama.StringEncoder(e.BatchID.String()),
			Value: sarama.ByteEncoder(data),
		}
	default:
		return fmt.Errorf("unknown event type: %T", event)
	}

	// 发送消息
	_, _, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		// ✅ 判断是否是临时性错误
		if isTemporaryError(err) {
			// 指数退避：100ms, 200ms, 400ms, 800ms, 1600ms
			backoff := 100 * time.Millisecond * (1 << uint(attempt))
			time.Sleep(backoff)
			return k.publishWithRetry(ctx, event, attempt+1)
		}

		// ✅ 永久性错误，直接返回失败
		return err
	}

	return nil
}

// isTemporaryError 判断错误是否是临时性的
func isTemporaryError(err error) bool {
	if err == nil {
		return false
	}
	// Kafka 临时错误通常是网络相关的
	// 这里可以根据具体需要细化判断
	return true
}
// sendToDLQ 将失败的事件发送到死信队列
func (k *kafkaEventProducer) sendToDLQ(ctx context.Context, event domain.DomainEvent, originalErr error) {
	if k.dlqProducer == nil {
		log.Printf("[DLQ] No DLQ producer configured, event dropped: %T, error: %v", event, originalErr)
		return
	}

	// 序列化事件
	data, err := json.Marshal(map[string]interface{}{
		"event":      event,
		"error":      originalErr.Error(),
		"timestamp":  time.Now().Format("2006-01-02T15:04:05Z07:00"),
	})
	if err != nil {
		log.Printf("[DLQ] Failed to marshal event: %v", err)
		return
	}

	// 发送到 DLQ topic
	dlqMsg := &sarama.ProducerMessage{
		Topic: k.dlqTopic,
		Value: sarama.ByteEncoder(data),
	}

	if _, _, err := k.dlqProducer.SendMessage(dlqMsg); err != nil {
		log.Printf("[DLQ] Failed to send to DLQ: %v", err)
		return
	}

	log.Printf("[DLQ] Event sent to DLQ successfully: %T", event)
}
// publishBatchCreated - 发布 BatchCreated 事件（小写，私有方法）
func (k *kafkaEventProducer) publishBatchCreated(ctx context.Context, event domain.BatchCreated) error {
	message := fmt.Sprintf(`{"event_type":"BatchCreated","batch_id":"%s","vehicle_id":"%s","vin":"%s","timestamp":"%s"}`,
		event.BatchID,
		event.VehicleID,
		event.VIN,
		event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
	)

	kafkaMsg := &sarama.ProducerMessage{
		Topic: k.topic,
		Key:   sarama.StringEncoder(event.BatchID.String()),
		Value: sarama.StringEncoder(message),
	}

	partition, offset, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("[Kafka] BatchCreated sent successfully. Partition: %d, Offset: %d", partition, offset)
	return nil
}

// publishStatusChanged - 发布 StatusChanged 事件（小写，私有方法）
func (k *kafkaEventProducer) publishStatusChanged(ctx context.Context, event domain.BatchStatusChanged) error {
	message := fmt.Sprintf(`{"event_type":"StatusChanged","batch_id":"%s","old_status":"%s","new_status":"%s","timestamp":"%s"}`,
		event.BatchID,
		event.OldStatus.String(),
		event.NewStatus.String(),
		event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
	)

	kafkaMsg := &sarama.ProducerMessage{
		Topic: k.topic,
		Key:   sarama.StringEncoder(event.BatchID.String()),
		Value: sarama.StringEncoder(message),
	}

	partition, offset, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("[Kafka] StatusChanged sent successfully. Partition: %d, Offset: %d", partition, offset)
	return nil
}

// publishFileParsed - 发布 FileParsed 事件（小写，私有方法）
func (k *kafkaEventProducer) publishFileParsed(ctx context.Context, event domain.FileParsed) error {
	message := fmt.Sprintf(`{"event_type":"FileParsed","batch_id":"%s","file_id":"%s","timestamp":"%s"}`,
		event.BatchID,
		event.FileID,
		event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
	)

	kafkaMsg := &sarama.ProducerMessage{
		Topic: k.topic,
		Key:   sarama.StringEncoder(event.BatchID.String()),
		Value: sarama.StringEncoder(message),
	}

	partition, offset, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("[Kafka] FileParsed sent successfully. Partition: %d, Offset: %d", partition, offset)
	return nil
}
// publishGatheringCompleted - 发布 GatheringCompleted 事件（Python Worker 调用）
func (k *kafkaEventProducer) publishGatheringCompleted(ctx context.Context, event domain.GatheringCompleted) error {
	// 定义 JSON 序列化结构（使用结构体 + json tag，性能更好）
	type GatheringCompletedEvent struct {
		EventType  string   `json:"event_type"`
		Version    string   `json:"version"`
		BatchID    string   `json:"batch_id"`
		TotalFiles int      `json:"total_files"`
		ChartFiles []string `json:"chart_files"`
		Timestamp  string   `json:"timestamp"`
	}

	kafkaEvent := GatheringCompletedEvent{
		EventType:  "GatheringCompleted",
		Version:    event.Version,
		BatchID:    event.BatchID.String(),
		TotalFiles: event.TotalFiles,
		ChartFiles: event.ChartFiles,
		Timestamp:  event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// JSON 序列化
	data, err := json.Marshal(kafkaEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// 验证消息大小
	if len(data) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max: %d)", len(data), maxMessageSize)
	}

	kafkaMsg := &sarama.ProducerMessage{
		Topic: k.topic,
		Key:   sarama.StringEncoder(event.BatchID.String()),
		Value: sarama.ByteEncoder(data),
	}

	partition, offset, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("[Kafka] GatheringCompleted sent successfully. batch=%s, charts=%d, partition=%d, offset=%d",
		event.BatchID, len(event.ChartFiles), partition, offset)
	return nil
}

// publishDiagnosisCompleted - 发布 DiagnosisCompleted 事件（AI Agent Worker 调用）
func (k *kafkaEventProducer) publishDiagnosisCompleted(ctx context.Context, event domain.DiagnosisCompleted) error {
	// 定义 JSON 序列化结构
	type DiagnosisCompletedEvent struct {
		EventType          string                       `json:"event_type"`
		Version            string                       `json:"version"`
		BatchID            string                       `json:"batch_id"`
		DiagnosisID        string                       `json:"diagnosis_id"`
		DiagnosisSummary   string                       `json:"diagnosis_summary"`
		TopErrorCodes      []domain.ErrorCodeSummary    `json:"top_error_codes"`
		TokenUsage         domain.TokenUsageInfo        `json:"token_usage"`
		Timestamp          string                       `json:"timestamp"`
	}

	kafkaEvent := DiagnosisCompletedEvent{
		EventType:        "DiagnosisCompleted",
		Version:          event.Version,
		BatchID:          event.BatchID.String(),
		DiagnosisID:      event.DiagnosisID.String(),
		DiagnosisSummary: event.DiagnosisSummary,
		TopErrorCodes:    event.TopErrorCodes,
		TokenUsage:       event.TokenUsage,
		Timestamp:        event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// JSON 序列化
	data, err := json.Marshal(kafkaEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// 验证消息大小
	if len(data) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max: %d)", len(data), maxMessageSize)
	}

	kafkaMsg := &sarama.ProducerMessage{
		Topic: k.topic,
		Key:   sarama.StringEncoder(event.BatchID.String()),
		Value: sarama.ByteEncoder(data),
	}

	partition, offset, err := k.producer.SendMessage(kafkaMsg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("[Kafka] DiagnosisCompleted sent successfully. batch=%s, diagnosis=%s, tokens=%d, cost=$%.4f, partition=%d, offset=%d",
		event.BatchID, event.DiagnosisID, event.TokenUsage.TotalTokens,
		event.TokenUsage.EstimatedCost, partition, offset)
	return nil
}
// Close - 关闭 Kafka Producer
func (k *kafkaEventProducer) Close() error {
	log.Printf("[Kafka] Closing producer...")
	return k.producer.Close()
}
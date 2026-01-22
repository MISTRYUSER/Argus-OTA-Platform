package kafka

import (
	"context"
	"fmt"
	"log"

	"github.com/IBM/sarama"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

// kafkaEventProducer - Kafka 事件发布器实现（小写，包私有）
type kafkaEventProducer struct {
	producer sarama.SyncProducer
	topic    string
}

// NewKafkaEventProducer - 创建 Kafka Producer
// 返回接口类型，而不是具体实现
func NewKafkaEventProducer(brokers []string, topic string) (messaging.KafkaEventPublisher, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer(brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka producer: %w", err)
	}

	log.Printf("[Kafka] Producer created successfully. Brokers: %v, Topic: %s", brokers, topic)

	return &kafkaEventProducer{
		producer: producer,
		topic:    topic,
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
		default:
			log.Printf("[Kafka] Unknown event type: %T", event)
		}
	}

	log.Printf("[Kafka] Successfully published %d events", len(events))
	return nil
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

// Close - 关闭 Kafka Producer
func (k *kafkaEventProducer) Close() error {
	log.Printf("[Kafka] Closing producer...")
	return k.producer.Close()
}
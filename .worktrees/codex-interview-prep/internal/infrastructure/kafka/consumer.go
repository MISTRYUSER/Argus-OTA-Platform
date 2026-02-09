package kafka

import (
	"context"
	"log"

	"github.com/IBM/sarama"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

type KafkaEventConsumer struct {
	consumer sarama.ConsumerGroup
	handler  messaging.MessageHandler
	topic    string
}

func NewKafkaEventConsumer(brokers []string, groupID string) (messaging.KafkaEventConsumer, error) {
	// create Saram config
	config := sarama.NewConfig()
	config.Version = sarama.V2_8_0_0
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

	// create consumer group
	consumer, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, err
	}
	return &KafkaEventConsumer{
		consumer: consumer,
	}, nil
}

// Subscribe - 订阅 Kafka 主题并消费消息
func (c *KafkaEventConsumer) Subscribe(ctx context.Context, topics []string, handler messaging.MessageHandler) error {
	c.handler = handler
	c.topic = topics[0] // 简化实现，假设只订阅一个 topic

	log.Printf("[Kafka] Starting consumer. Topics: %v", topics)

	// 创建 ConsumerGroupHandler 适配器
	groupHandler := &consumerGroupHandler{
		messageHandler: handler,
	}

	// 在后台 goroutine 中消费消息
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("[Kafka] Consumer context cancelled")
				return
			default:
				if err := c.consumer.Consume(ctx, topics, groupHandler); err != nil {
					log.Printf("[Kafka] Consumer error: %v", err)
				}
			}
		}
	}()

	log.Printf("[Kafka] Consumer subscribed successfully")
	return nil
}

// Close - 关闭 Kafka Consumer
func (c *KafkaEventConsumer) Close() error {
	log.Printf("[Kafka] Closing consumer...")
	return c.consumer.Close()
}

// consumerGroupHandler - 实现 sarama.ConsumerGroupHandler 接口
type consumerGroupHandler struct {
	messageHandler messaging.MessageHandler
}

// Setup - 在会话开始时调用
func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	return nil
}

// Cleanup - 在会话结束时调用
func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim - 消费消息
func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if err := h.messageHandler(context.Background(), msg.Value); err != nil {
				log.Printf("Message handler failed: %v", err)
			}

			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}
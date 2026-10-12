package kafka

import (
	"context"
	"errors"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	"log"
	"sync"
	"time"
)

type KafkaEventConsumer struct {
	store    messaging.DeadLetterStore
	consumer sarama.ConsumerGroup
	brokers  []string
	dlq      messaging.KafkaEventPublisher
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	failure  messaging.FailureHandler
}

func NewKafkaEventConsumer(brokers []string, groupID string, options ...ConsumerOption) (messaging.KafkaEventConsumer, error) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	c, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return nil, err
	}
	wrapper := &KafkaEventConsumer{consumer: c, brokers: brokers}
	for _, option := range options {
		option(wrapper)
	}
	return wrapper, nil
}
func (c *KafkaEventConsumer) Subscribe(ctx context.Context, topics []string, handler messaging.MessageHandler) error {
	if len(topics) == 0 || handler == nil {
		return fmt.Errorf("topics and handler are required")
	}
	if c.cancel != nil {
		return fmt.Errorf("already subscribed")
	}
	dlq, err := NewKafkaEventProducer(c.brokers, topics[0]+"-dlq", "")
	if err != nil {
		return err
	}
	c.dlq = dlq
	ctx, c.cancel = context.WithCancel(ctx)
	h := &consumerGroupHandler{messageHandler: handler, dlq: dlq, failure: c.failure, store: c.store}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for ctx.Err() == nil {
			if err := c.consumer.Consume(ctx, topics, h); err != nil && ctx.Err() == nil {
				log.Printf("consumer retry: %v", err)
				if err = waitRetry(ctx); err != nil {
					return
				}
			}
		}
	}()
	return nil
}
func (c *KafkaEventConsumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	err := c.consumer.Close()
	c.wg.Wait()
	if c.dlq != nil {
		if e := c.dlq.Close(); err == nil {
			err = e
		}
	}
	return err
}
func waitRetry(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type consumerGroupHandler struct {
	store          messaging.DeadLetterStore
	failure        messaging.FailureHandler
	messageHandler messaging.MessageHandler
	dlq            messaging.KafkaEventPublisher
}

func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *consumerGroupHandler) quarantine(ctx context.Context, msg *sarama.ConsumerMessage, cause error, attempts int) error {
	if h.failure != nil {
		if err := h.failure(ctx, msg.Value, cause); err != nil {
			return err
		}
	}
	if h.dlq == nil {
		return fmt.Errorf("DLQ unavailable")
	}
	metadata := map[string]interface{}{"original_topic": msg.Topic, "partition": msg.Partition, "offset": msg.Offset, "reason": domain.SafeText(cause.Error(), 1500), "attempts": attempts, "payload_bytes": len(msg.Value)}
	if h.store != nil {
		reference, err := h.store(ctx, msg.Topic, msg.Partition, msg.Offset, msg.Value, cause.Error())
		if err != nil {
			return err
		}
		metadata["payload_reference"] = reference
		preview := msg.Value
		if len(preview) > 512 {
			preview = preview[:512]
		}
		metadata["payload_prefix_base64"] = preview
		metadata["inline_payload_truncated"] = len(preview) != len(msg.Value)
	} else if len(msg.Value) <= 256*1024 {
		metadata["payload_base64"] = msg.Value
	} else {
		return fmt.Errorf("durable DLQ store required for large message")
	}
	return h.dlq.PublishEvents(ctx, []domain.DomainEvent{domain.WireEvent{Type: "DeadLetter", Timestamp: time.Now(), Data: metadata}})
}
func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ctx := session.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			attempts := 0
			for {
				attempts++
				err := h.messageHandler(ctx, msg.Value)
				if err == nil {
					session.MarkMessage(msg, "")
					break
				}
				var malformed *domain.InvalidEventError
				var terminal *domain.PermanentTaskError
				if errors.As(err, &malformed) || errors.As(err, &terminal) || attempts >= 5 {
					for {
						if h.quarantine(ctx, msg, err, attempts) == nil {
							session.MarkMessage(msg, "")
							break
						}
						if waitRetry(ctx) != nil {
							return nil
						}
					}
					break
				}
				if waitRetry(ctx) != nil {
					return nil
				}
			}
		}
	}
}

type ConsumerOption func(*KafkaEventConsumer)

func WithFailureHandler(handler messaging.FailureHandler) ConsumerOption {
	return func(c *KafkaEventConsumer) { c.failure = handler }
}

func WithDeadLetterStore(store messaging.DeadLetterStore) ConsumerOption {
	return func(c *KafkaEventConsumer) { c.store = store }
}

package messaging

import (
	"context"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

// KafkaEventPublisher - Kafka 事件发布器接口
type KafkaEventPublisher interface {
	// PublishEvents - 批量发布领域事件到 Kafka
	PublishEvents(ctx context.Context, events []domain.DomainEvent) error

	// Close - 关闭 Kafka Producer 连接
	Close() error
}

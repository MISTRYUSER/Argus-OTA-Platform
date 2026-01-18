package application
import (
	"context"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

type KafkaEventPublisher interface {
	PublishBatchCreated(ctx context.Context, event domain.BatchCreated) error

	PublishStatusChanged(ctx context.Context, event domain.BatchStatusChanged) error

	PublishEvents(ctx context.Context, events []domain.DomainEvent) error
}
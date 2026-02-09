package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

type EventPublisher struct {
	client *redis.Client
}

func NewEventPublisher(client *redis.Client) domain.EventPublisher {
	return &EventPublisher{client : client}
}

//SSE
func (p *EventPublisher) PublishProgress(ctx context.Context, batchID string, event domain.StreamEvent) error {
	channel := fmt.Sprintf("batch:%s:progress",batchID)
	data,err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event : %w",err)
	}
	result := p.client.Publish(ctx,channel,data)
	if result.Err() != nil {
		return fmt.Errorf("failed to publish event: %w", result.Err())
	}
	return nil
}
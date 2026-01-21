package messaging
import (
	"context"
)
type KafkaEventConsumer interface {
	Subscribe(ctx context.Context,topics []string,handler MessageHandler) error 
	Close() error
}

  // 2. MessageHandler 回调函数类型
  type MessageHandler func(ctx context.Context, event []byte) error
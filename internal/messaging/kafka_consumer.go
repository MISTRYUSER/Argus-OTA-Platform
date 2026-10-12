package messaging

import (
	"context"
)

type KafkaEventConsumer interface {
	Subscribe(ctx context.Context, topics []string, handler MessageHandler) error
	Close() error
}

// 2. MessageHandler 回调函数类型
type MessageHandler func(ctx context.Context, event []byte) error

// FailureHandler persists terminal task state before a failed delivery is quarantined.
type FailureHandler func(context.Context, []byte, error) error

// DeadLetterStore retains the complete failed payload and returns its durable reference.
type DeadLetterStore func(context.Context, string, int32, int64, []byte, string) (string, error)

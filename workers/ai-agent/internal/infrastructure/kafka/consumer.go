package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	"time"
)

type BatchDiagnoser interface {
	DiagnoseBatch(context.Context, string) error
}
type MessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}
type Consumer struct {
	reader  MessageReader
	service BatchDiagnoser
}

func NewConsumer(reader MessageReader, service BatchDiagnoser) *Consumer {
	return &Consumer{reader, service}
}
func (c *Consumer) HandleMessage(ctx context.Context, data []byte) error {
	var e struct {
		Type    string `json:"event_type"`
		BatchID string `json:"batch_id"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return &core.InvalidEventError{Err: fmt.Errorf("invalid JSON: %w", err)}
	}
	if e.Type != "DiagnosisRequested" {
		return nil
	}
	if _, err := uuid.Parse(e.BatchID); err != nil {
		return &core.InvalidEventError{Err: fmt.Errorf("invalid batch_id: %w", err)}
	}
	return c.service.DiagnoseBatch(ctx, e.BatchID)
}
func (c *Consumer) Consume(ctx context.Context) error {
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		for {
			if err = c.HandleMessage(ctx, msg.Value); err == nil {
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err = c.reader.CommitMessages(ctx, msg); err != nil {
			return err
		}
	}
}
func (c *Consumer) Close() error { return c.reader.Close() }

func (c *Consumer) HandleFailure(ctx context.Context, body []byte, cause error) error {
	var malformed *core.InvalidEventError
	if errors.As(cause, &malformed) {
		return nil
	}
	var e struct {
		Type string `json:"event_type"`
		ID   string `json:"batch_id"`
	}
	if json.Unmarshal(body, &e) != nil || e.Type != "DiagnosisRequested" {
		return nil
	}
	id, err := uuid.Parse(e.ID)
	if err != nil {
		return nil
	}
	recorder, ok := c.service.(interface {
		FailBatch(context.Context, string, error) error
	})
	if !ok {
		return fmt.Errorf("failure recorder unavailable")
	}
	return recorder.FailBatch(ctx, id.String(), cause)
}

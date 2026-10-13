package kafka

import (
	"context"
	"github.com/IBM/sarama"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"strings"
)

func producerTraceHeaders(ctx context.Context) []sarama.RecordHeader {
	out := []sarama.RecordHeader{}
	for k, v := range obs.Headers(ctx) {
		out = append(out, sarama.RecordHeader{Key: []byte(k), Value: []byte(v)})
	}
	return out
}
func consumerTraceContext(ctx context.Context, headers []*sarama.RecordHeader) context.Context {
	carrier := map[string]string{}
	for _, h := range headers {
		if h == nil || len(h.Value) > 512 {
			continue
		}
		key := strings.ToLower(string(h.Key))
		if key == "traceparent" || key == "tracestate" {
			carrier[key] = string(h.Value)
		}
	}
	return obs.Extract(ctx, carrier)
}
func (h *consumerGroupHandler) observedMessage(ctx context.Context, msg *sarama.ConsumerMessage) (err error) {
	ctx, end := obs.Operation(consumerTraceContext(ctx, msg.Headers), "kafka.consume")
	defer func() { end(err) }()
	return h.messageHandler(ctx, msg.Value)
}

package kafka

import (
	"context"
	"github.com/IBM/sarama"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"testing"
)

func TestKafkaTraceContextRoundTrip(t *testing.T) {
	p := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	ctx, end := obs.Operation(context.Background(), "task")
	defer end(nil)
	headers := producerTraceHeaders(ctx)
	consumer := []*sarama.RecordHeader{}
	for i := range headers {
		h := headers[i]
		consumer = append(consumer, &h)
	}
	remote := consumerTraceContext(context.Background(), consumer)
	if trace.SpanContextFromContext(remote).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
		t.Fatal("Kafka propagation lost trace")
	}
}

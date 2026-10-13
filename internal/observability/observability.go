// Package observability provides opt-in standard OTel export; business operations
// never depend on successful telemetry delivery.
package observability

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	metricHTTP "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	traceHTTP "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
)

// Opaque process identity separates cumulative metrics from independent replicas.
// Keep it stable for this process; avoid hostnames, VINs or per-task identifiers.
var processInstance = uuid.NewString()

func instanceIdentity(configured string) string {
	if configured != "" {
		return configured
	}
	return processInstance
}

type agentKey struct{}
type agentBatchKey struct{}
type Config struct {
	Endpoint, Service, Instance string
	MetricInterval              time.Duration
}

func AgentContext(ctx context.Context, batch ...string) context.Context {
	ctx = context.WithValue(ctx, agentKey{}, true)
	if len(batch) > 0 {
		ctx = context.WithValue(ctx, agentBatchKey{}, batch[0])
	}
	return ctx
}
func Headers(ctx context.Context) map[string]string {
	c := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, c)
	return c
}
func Extract(ctx context.Context, headers map[string]string) context.Context {
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier(headers))
}
func Logger(ctx context.Context) *slog.Logger {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return slog.Default()
	}
	return slog.Default().With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
}
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
func Operation(ctx context.Context, operation string, attrs ...attribute.KeyValue) (context.Context, func(error)) {
	// Instrument names and metric labels stay in this bounded vocabulary.
	switch operation {
	case "task", "model", "tool", "kafka.produce", "kafka.consume", "http":
	default:
		operation = "other"
	}
	kind := "span"
	switch operation {
	case "task":
		kind = "agent"
	case "model":
		kind = "generation"
	case "tool":
		kind = "tool"
	}
	attrs = append(attrs, attribute.String("langfuse.observation.type", kind))
	attrs = append(attrs, attribute.Bool("argus.agent", ctx.Value(agentKey{}) == true))
	if ctx.Value(agentKey{}) == true {
		attrs = append(attrs, attribute.String("langfuse.trace.name", "argus.investigation"), attribute.String("langfuse.version", os.Getenv("ARGUS_RELEASE")))
		if batch, ok := ctx.Value(agentBatchKey{}).(string); ok {
			attrs = append(attrs, attribute.String("langfuse.session.id", batch))
		}
	}
	ctx, span := otel.Tracer("argus").Start(ctx, "argus."+operation, trace.WithAttributes(attrs...))
	start := time.Now()
	return ctx, func(err error) {
		outcome := "ok"
		if err != nil {
			outcome = "error"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				outcome = "canceled"
			}
			span.SetStatus(codes.Error, outcome)
			span.SetAttributes(attribute.String("error.type", outcome))
		}
		labels := metric.WithAttributes(attribute.String("operation", operation), attribute.String("outcome", outcome))
		counter, _ := otel.Meter("argus").Int64Counter("argus_operations_total")
		counter.Add(ctx, 1, labels)
		duration, _ := otel.Meter("argus").Float64Histogram("argus_operation_duration_seconds", metric.WithUnit("s"))
		duration.Record(ctx, time.Since(start).Seconds(), labels)
		Logger(ctx).Info("operation_completed", "operation", operation, "outcome", outcome, "duration_ms", time.Since(start).Milliseconds())
		span.End()
	}
}
func AddTokens(ctx context.Context, n int) {
	if n > 0 {
		c, _ := otel.Meter("argus").Int64Counter("argus_model_tokens_total")
		c.Add(ctx, int64(n))
	}
}
func AddCounter(ctx context.Context, name string) {
	if name != "argus_model_queue_rejections_total" && name != "argus_model_retries_total" {
		return
	}
	c, _ := otel.Meter("argus").Int64Counter(name)
	c.Add(ctx, 1)
}
func Setup(ctx context.Context, c Config) (func(context.Context) error, error) {
	if c.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" {
		return nil, fmt.Errorf("invalid OTLP base endpoint")
	}
	if len(c.Instance) > 128 {
		return nil, fmt.Errorf("instance identity exceeds128bytes")
	}
	if c.Service == "" {
		return nil, fmt.Errorf("service name required")
	}
	if c.MetricInterval <= 0 {
		c.MetricInterval = 10 * time.Second
	}
	base := strings.TrimRight(c.Endpoint, "/")
	te, e := traceHTTP.New(ctx, traceHTTP.WithEndpointURL(base+"/v1/traces"), traceHTTP.WithTimeout(2*time.Second))
	if e != nil {
		return nil, e
	}
	me, e := metricHTTP.New(ctx, metricHTTP.WithEndpointURL(base+"/v1/metrics"), metricHTTP.WithTimeout(2*time.Second))
	if e != nil {
		te.Shutdown(ctx)
		return nil, e
	}
	r := resource.NewSchemaless(attribute.String("service.name", c.Service), attribute.String("service.instance.id", instanceIdentity(c.Instance)), attribute.String("service.version", os.Getenv("ARGUS_RELEASE")), attribute.String("k8s.namespace.name", namespace()))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(te), sdktrace.WithResource(r))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(c.MetricInterval))), sdkmetric.WithResource(r))
	oldT, oldM := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		defer otel.SetTracerProvider(oldT)
		defer otel.SetMeterProvider(oldM)
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}
func SetupFromEnv(ctx context.Context, service string) (func(context.Context) error, error) {
	if os.Getenv("ARGUS_LOG_FORMAT") == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service_name", service, "namespace", namespace()))
	}
	return Setup(ctx, Config{Endpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), Service: service, Instance: os.Getenv("ARGUS_INSTANCE_ID")})
}

func namespace() string {
	if s := os.Getenv("ARGUS_NAMESPACE"); s != "" {
		return s
	}
	return "argus"
}

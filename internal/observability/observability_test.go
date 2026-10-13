package observability

import (
	"context"
	"encoding/json"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOperationsExportSafeAttributesAndPropagate(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	ctx, end := Operation(AgentContext(context.Background()), "task", attribute.String("argus.batch_id", "fixture"))
	child, finish := Operation(ctx, "model")
	if trace.SpanContextFromContext(child).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
		t.Fatal("trace linkage missing")
	}
	carrier := Headers(ctx)
	linked := Extract(context.Background(), carrier)
	if trace.SpanContextFromContext(linked).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
		t.Fatal("carrier lost parent")
	}
	finish(nil)
	end(nil)
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans=%d", len(spans))
	}
	for _, s := range spans {
		marked := false
		for _, a := range s.Attributes {
			if a.Key == "argus.agent" && a.Value.AsBool() {
				marked = true
			}
		}
		if !marked {
			t.Fatal("Agent filtering marker absent")
		}
	}
}
func TestOTLPExportProtocol(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) == 0 {
			t.Error("empty OTLP payload")
		}
		requests <- r.URL.Path
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer server.Close()
	shutdown, e := Setup(context.Background(), Config{Endpoint: server.URL, Service: "argus-test", MetricInterval: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	ctx, end := Operation(AgentContext(context.Background()), "task")
	AddTokens(ctx, 123)
	end(nil)
	timeout, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = shutdown(timeout); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for len(requests) > 0 {
		seen[<-requests] = true
	}
	if !seen["/v1/traces"] || !seen["/v1/metrics"] {
		b, _ := json.Marshal(seen)
		t.Fatalf("export missing %s", b)
	}
}

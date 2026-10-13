package main

import (
	"context"
	"encoding/json"
	"errors"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailedBenchmarkKeepsTraceWithoutDiagnosis(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	for _, mode := range []string{"agent", "baseline"} {
		ctx, end := benchmarkContext(context.Background(), "controlled-failed-trial", mode)
		// A model error returns no DiagnosisResult; the row must still use this ID.
		id := obs.TraceID(ctx)
		end(errors.New("controlled model failure"))
		if len(id) != 32 {
			t.Fatalf("%s failed trial missing trace ID", mode)
		}
		spans := exporter.GetSpans()
		span := spans[len(spans)-1]
		if span.SpanContext.TraceID().String() != id || span.Status.Code != codes.Error {
			t.Fatal("failure row is not correlated with error span")
		}
	}
}

func TestRequestTraceRecordsActual429WithoutHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	transport := &requestTrace{}
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"tool_choice":"auto","tools":[{"function":{"name":"read_logs"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "private-test-credential")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(transport.rows) != 1 || transport.rows[0]["http_status"] != 429 {
		t.Fatal("actual429 missing")
	}
	body, err := json.Marshal(transport.rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-test-credential") || strings.Contains(string(body), "Authorization") {
		t.Fatal("headers leaked into receipt")
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/schema"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/nodes"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
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

func TestInvalidBaselineReplyRemainsInRawEvidence(t *testing.T) {
	content := `{"root_cause":"truncated`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]string{"role": "assistant", "content": content}}}})
	}))
	defer server.Close()
	chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	observer := &briefModel{ChatModel: chat}
	reply, err := observer.BaselineGenerate(context.Background(), []*schema.Message{schema.UserMessage("controlled invalid JSON fixture")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = nodes.ParseDiagnosisResult(reply.Content); err == nil {
		t.Fatal("fixture must fail parsing")
	}
	if len(observer.Replies) != 1 || observer.Replies[0].Content != content {
		t.Fatal("invalid model response lost after parse failure")
	}
}

package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	query "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/telemetryquery"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestAgentTelemetryInvestigationExportsExecutionTrace(t *testing.T) {
	runTelemetryInvestigation(t, &signalFixture{}, uuid.NewString(), "cpu", "", "")
}

// Opt-in integration: real Prometheus/Loki/Tempo, scripted model (no paid API).
func TestAgentLiveTelemetryInvestigation(t *testing.T) {
	path := os.Getenv("ARGUS_LIVE_TELEMETRY_RECEIPT")
	if path == "" {
		t.Skip("real middleware receipt not configured")
	}
	var receipt struct {
		Batch string               `json:"batch_id"`
		Scope domain.IncidentScope `json:"scope"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewClient(query.Config{PrometheusURL: "http://127.0.0.1:9090", LokiURL: "http://127.0.0.1:3100", TempoURL: "http://127.0.0.1:3200", AllowedMetrics: []string{"argus_operations_total"}}, func(_ context.Context, id string) (domain.IncidentScope, error) {
		if id != receipt.Batch {
			return domain.IncidentScope{}, fmt.Errorf("incorrect scope")
		}
		return receipt.Scope, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runTelemetryInvestigation(t, reader, receipt.Batch, "argus_operations_total", receipt.Batch, os.Getenv("ARGUS_LIVE_TELEMETRY_RESULT"))
}

func runTelemetryInvestigation(t *testing.T, reader domain.TelemetryReader, batch, metric, contains, output string) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	options := []sdktrace.TracerProviderOption{sdktrace.WithSyncer(exporter), sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "argus-agent-live-test"), attribute.String("k8s.namespace.name", "argus")))}
	if output != "" {
		endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if endpoint == "" {
			t.Fatal("OTLP endpoint required for live execution trace")
		}
		remote, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"))
		if err != nil {
			t.Fatal(err)
		}
		options = append(options, sdktrace.WithBatcher(remote))
	}
	tp := sdktrace.NewTracerProvider(options...)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(old)
	calls := 0
	call := func(name, args string) map[string]interface{} {
		return map[string]interface{}{"id": uuid.NewString(), "type": "function", "function": map[string]string{"name": name, "arguments": args}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		message := map[string]interface{}{"role": "assistant", "content": `{"hypothesis_id":"H1","root_cause":"probe signals gathered","suggestions":["inspect"],"severity":"info","confidence":0.1,"evidence_ids":["log:1","metric:fixture","trace:fixture"],"limitations":["protocol fixture, not diagnosis quality"]}`}
		if output != "" {
			message["content"] = `{"hypothesis_id":"H1","root_cause":"transport verification only","suggestions":["inspect"],"severity":"info","confidence":0.1,"evidence_ids":["log:1"],"limitations":["scripted model and synthetic signals; no diagnosis quality claim"]}`
		}
		if calls == 1 {
			message["content"] = ""
			message["tool_calls"] = []interface{}{call("read_logs", `{}`), call("query_metrics", fmt.Sprintf(`{"metric":%q}`, metric)), call("query_service_logs", fmt.Sprintf(`{"contains":%q}`, contains)), call("query_traces", `{"status":"error"}`)}
		}
		if calls == 2 {
			message["content"] = ""
			message["tool_calls"] = []interface{}{call("error_statistics", `{}`), call("update_investigation_plan", `{"hypotheses":[{"id":"H1","description":"probe signals observed","status":"open","next_check":"confirm cause"}]}`)}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "message": message}}})
	}))
	defer server.Close()
	chat, e := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	a, e := NewAgent(repositoryFixture{}, nil, chat, Options{MaxSteps: 5, Telemetry: reader})
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.Run(context.Background(), batch)
	if e != nil {
		t.Fatal(e)
	}
	if len(result.ExecutionTraceID) != 32 || len(result.ToolTrace) != 6 {
		t.Fatalf("missing trace/evidence %+v", result)
	}
	if output != "" {
		for _, observation := range result.ToolTrace {
			if observation.Tool == "query_metrics" || observation.Tool == "query_service_logs" || observation.Tool == "query_traces" {
				if observation.Error != "" {
					t.Fatalf("real backend tool error: %s", observation.Error)
				}
				var evidence struct {
					Evidence []domain.EvidenceItem `json:"evidence"`
				}
				if err := json.Unmarshal(observation.Output, &evidence); err != nil {
					t.Fatal(err)
				}
				if len(evidence.Evidence) == 0 {
					t.Fatalf("%s returned no live evidence: %s", observation.Tool, observation.Output)
				}
			}
		}
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(output, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != 10 {
		t.Fatalf("want task+3model+6tools, got%d", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext.TraceID().String() != result.ExecutionTraceID {
			t.Fatal("unlinked execution trace")
		}
	}
}

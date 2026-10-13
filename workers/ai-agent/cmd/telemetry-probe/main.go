// telemetry-probe verifies real middleware with synthetic, explicitly labelled data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/google/uuid"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	query "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/telemetryquery"
	"go.opentelemetry.io/otel/attribute"
	"os"
	"time"
)

type receipt struct {
	Batch    string                           `json:"batch_id"`
	Trace    string                           `json:"trace_id"`
	Scope    domain.IncidentScope             `json:"scope"`
	Evidence map[string][]domain.EvidenceItem `json:"evidence,omitempty"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	phase := flag.String("phase", "emit", "emit or query")
	path := flag.String("out", "", "receipt JSON")
	flag.Parse()
	if *path == "" {
		return fmt.Errorf("receipt path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if *phase == "emit" {
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
			return fmt.Errorf("OTLP endpoint required")
		}
		shutdown, e := obs.SetupFromEnv(ctx, "argus-telemetry-probe")
		if e != nil {
			return e
		}
		id := uuid.NewString()
		r := receipt{Batch: id, Scope: domain.IncidentScope{Namespace: "argus", Service: "argus-telemetry-probe", Start: time.Now().UTC().Add(-time.Minute), End: time.Now().UTC().Add(5 * time.Minute)}}
		ctx, end := obs.Operation(obs.AgentContext(ctx, id), "task", attribute.Bool("argus.synthetic_probe", true))
		r.Trace = obs.TraceID(ctx)
		tool, done := obs.Operation(ctx, "tool", attribute.String("tool.name", "synthetic_probe"))
		obs.Logger(tool).Info("controlled telemetry probe", "probe_id", id)
		done(fmt.Errorf("synthetic probe error for transport verification"))
		end(nil)
		if e = shutdown(ctx); e != nil {
			return e
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		return os.WriteFile(*path, b, 0644)
	}
	if *phase != "query" {
		return fmt.Errorf("invalid phase")
	}
	b, e := os.ReadFile(*path)
	if e != nil {
		return e
	}
	var r receipt
	if e = json.Unmarshal(b, &r); e != nil {
		return e
	}
	reader, e := query.NewClient(query.Config{PrometheusURL: "http://127.0.0.1:9090", LokiURL: "http://127.0.0.1:3100", TempoURL: "http://127.0.0.1:3200", AllowedMetrics: []string{"argus_operations_total"}}, func(_ context.Context, id string) (domain.IncidentScope, error) {
		if id != r.Batch {
			return domain.IncidentScope{}, fmt.Errorf("wrong scope")
		}
		return r.Scope, nil
	})
	if e != nil {
		return e
	}
	r.Evidence = map[string][]domain.EvidenceItem{}
	r.Evidence["metrics"], e = reader.QueryMetrics(ctx, r.Batch, domain.MetricQuery{Metric: "argus_operations_total", Limit: 10})
	if e != nil {
		return e
	}
	r.Evidence["logs"], e = reader.QueryLogs(ctx, r.Batch, domain.LogQuery{Contains: r.Batch, Limit: 10})
	if e != nil {
		return e
	}
	r.Evidence["traces"], e = reader.QueryTraces(ctx, r.Batch, domain.TraceQuery{Status: "error", Limit: 10})
	if e != nil {
		return e
	}
	for kind, items := range r.Evidence {
		if len(items) == 0 {
			return fmt.Errorf("no scoped %s evidence", kind)
		}
	}
	b, _ = json.MarshalIndent(r, "", "  ")
	return os.WriteFile(*path, b, 0644)
}

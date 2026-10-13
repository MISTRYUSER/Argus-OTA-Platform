# Two-layer telemetry and evaluation

User authorized both platform self-observability and incident evidence queries, using open-source middleware, with regression after each stage.

Architecture: Go OpenTelemetry spans/metrics and structured slog JSON -> OTel Collector -> Prometheus/Loki/Tempo -> Grafana. Optional Langfuse OTLP/HTTP receives only Agent spans and links experiment scores, using existing PostgreSQL/Redis/S3-compatible storage plus ClickHouse. Deployment is local/opt-in; no default cloud uploads. Existing execution checkpoints stay application-level.

Evidence layer: domain TelemetryReader provides metrics/logs/traces methods bound to batch ID. Trusted DB IncidentScope sets namespace/service/start/end; tool arguments cannot override scope. Query builders allowlisted metrics, escaped label values, bounded time window/results/response bytes, context cancellation and endpoint timeout. Unrelated namespaces rejected before retrieval; no raw query language/SQL supplied by model. Add immutable incident binding migration.

Agent tools: query_metrics/query_service_logs/query_traces, optional when reader configured; outputs stable hashed evidence IDs and provenance. Limit per-run queries from durable observations. Toolset policy enters checkpoint compatibility checks. Local log requirement remains for backward compatibility.

Self-observability: task, model and tool spans; propagate W3C context in Kafka headers; metrics use bounded labels, never batch/VIN/prompt as metric labels. Structured logs include trace/span IDs without prompt, headers or API keys. Export failures do not fail diagnosis. Eval joins dataset/case/release/model/run to execution trace; source snapshots and failure rows retained.

Acceptance: middleware configs checked, scoped HTTP adapter tests, OTLP protocol export tests, end-to-end fixture showing model-selected all-three telemetry tools and exported execution trace, regression/race/vet/build. Actual middleware boot must be verified separately from protocol fixtures; report unavailable container runtime honestly if native alternatives cannot boot it. No production/accuracy improvement claim without fresh benchmark evidence.

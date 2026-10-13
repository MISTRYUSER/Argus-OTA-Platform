# Local platform observability and Agent evaluation

The platform emits OpenTelemetry task/model/tool spans and bounded operation counters and duration histograms. JSON slog records contain `trace_id` and `span_id`. A local Collector sends all traces to Tempo, metrics to Prometheus, and OTLP/file logs to Loki. Grafana provisions these three sources and links logs to traces. The optional Langfuse v4 pipeline copies **only** spans marked `argus.agent=true` to a local Agent UI. Mark the Agent root and every child; filtering only model spans would lose the root.

This Compose project is independent of `deployments/docker-compose.yml`. All published ports bind `127.0.0.1`. No external telemetry backend or model API is configured. Anonymous Grafana access is viewer-only on loopback. These single-node filesystem backends and retention settings are for local development; production needs authentication, TLS, HA/object storage, backup, capacity planning and service-level retention decisions. Application database checkpoints remain the durable execution state.

## Start and verify

Requires Docker with Compose v2. Run from the repository root:

```sh
mkdir -p /tmp/argus-observability-logs
export ARGUS_LOG_DIR=/tmp/argus-observability-logs
export OTEL_SERVICE_NAME=argus-ai-agent
docker compose -f deployments/observability/compose.yaml config --quiet
docker compose -f deployments/observability/compose.yaml up -d
python3 scripts/observability/smoke.py --log-dir "$ARGUS_LOG_DIR"
```

The application should export OTLP/HTTP to `http://127.0.0.1:4318` (base URL; traces and metrics append `/v1/traces` and `/v1/metrics`). Write JSON slog output to a `*.jsonl` file in `ARGUS_LOG_DIR`. The Collector parses `time`, `level`, `trace_id`, and `span_id`, preserving the original JSON line. The default file-log service is `argus-ai-agent`; give each service its own Collector/log directory or extend the receiver resource mapping before mixing services. Collector offsets and exporter queues persist in its own volume. A one-shot BusyBox init service owns only that named volume and sets its ownership to the verified Collector image UID/GID `10001:10001`; the Collector itself runs as that nonroot user and starts only after init succeeds. The init filesystem is read-only apart from its volume and its Linux capabilities are limited to ownership changes and traversing existing queue directories. Do not put prompts, authorization headers, API keys, or VINs into these logs.

Open Grafana at <http://127.0.0.1:3001>, dashboard “Argus platform and Agent.” Backends: Prometheus `:9090`, Loki `:3100`, Tempo `:3200`. Dashboard metric names match the application: `argus_operations_total`, `argus_operation_duration_seconds_bucket`, `argus_model_tokens_total`, `argus_model_retries_total`, and `argus_model_queue_rejections_total`. Metric labels are bounded `operation`/`outcome` plus explicitly selected resource `service_name`/`namespace`; no case, batch, VIN, prompt, or trace ID labels. Resource attributes are not promoted wholesale to Prometheus labels. Collector transforms select `service.name` and `k8s.namespace.name`; JSON log `service_name`/`namespace` override the file receiver defaults. Loki indexes the namespace alias for the scoped query adapter. Root logs carry trace IDs for Grafana correlation. Trace-to-metrics service maps need a separately enabled span metrics/service graph generator; its source link is provisioned but this stack does not produce service graph metrics.

The smoke command sends a unique trace, metric and structured log, then queries every backend for receipt. Supplying `--log-dir` additionally verifies filelog ingestion through the mounted directory. The command returns nonzero when any signal is missing. It exercises transport/storage, not application or Agent diagnosis quality. It requires the services to have actually started.

Static checks (PyYAML required) and exporter protocol tests:

```sh
python3 scripts/observability/check_config.py
python3 -m unittest discover -s scripts/benchmark -p test_export_langfuse.py -v
```

Static parsing/cross-reference tests are not backend configuration-schema validation. On a Docker host also run Collector `validate`, Prometheus `promtool check config`, and actual boot/smoke. Native Loki/Tempo/Grafana boot is the authority for configuration compatibility. The base stack was subsequently booted on an isolated Colima VM with Docker and the standalone Compose executable. Native Collector validation and the live smoke check passed for traces, metrics, OTLP logs and JSON file logs; the storage-init ownership fix was exercised on the real nonroot Collector. This proves the local signal path, not production reliability or diagnosis accuracy. Optional Langfuse validation uses separate disposable dependency instances as described below; it does not certify reuse of any existing PostgreSQL/Redis/MinIO instance.

## Optional Langfuse v4 using existing storage

The overlay adds Langfuse web/worker and dedicated ClickHouse, and explicitly reuses your existing PostgreSQL, Redis and MinIO. It creates no replacement database/cache/blob servers. Provision a **separate `langfuse` PostgreSQL database and login** outside this project and grant only that database; use a dedicated private `argus-langfuse` MinIO bucket and bucket-scoped credentials. Redis must support BullMQ and `noeviction`; namespace `argus-langfuse:` separates its keys. If the existing Redis eviction/auth settings are unsuitable, use a separate Redis instance instead of changing the application cache silently. Do not reuse the Argus application database or public log bucket.

Supply environment variables explicitly; the files have no fallback passwords and do not read existing secret files:

- `LANGFUSE_DATABASE_URL`: e.g. `postgresql://<dedicated-user>:<password>@host.docker.internal:5432/langfuse` (URL-encode credentials).
- `LANGFUSE_REDIS_URL`: authenticated Redis URL, e.g. `redis://<user>:<password>@host.docker.internal:6379/0`.
- `LANGFUSE_S3_ENDPOINT`: container-reachable MinIO URL; `LANGFUSE_S3_PUBLIC_ENDPOINT`: browser-reachable MinIO URL. Dedicated bucket `argus-langfuse` must already exist.
- `LANGFUSE_S3_ACCESS_KEY_ID`, `LANGFUSE_S3_SECRET_ACCESS_KEY`.
- `LANGFUSE_CLICKHOUSE_PASSWORD`, `LANGFUSE_SALT`, `LANGFUSE_NEXTAUTH_SECRET`, `LANGFUSE_ENCRYPTION_KEY` (64 hex characters; generate random values).
- `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`: chosen initial project keys, with Langfuse key prefixes `pk-lf-`/`sk-lf-`; `LANGFUSE_AUTH_BASE64`: base64 of `<public-key>:<secret-key>` (no newline).
- `LANGFUSE_INIT_USER_EMAIL`, `LANGFUSE_INIT_USER_PASSWORD` for the initial local account; `LANGFUSE_INIT_USER_NAME` is optional.

Web startup uses Langfuse's supported headless initialization variables to create the local organization/project/user on a fresh database. Already initialized databases retain their existing projects; keys must match the project you intend to use. Passwords/API keys should be provided via your shell or local secret manager, never committed. The optional Compose file is deliberately excluded from the base command, so missing Langfuse secrets do not prevent the normal stack from starting.

```sh
docker compose -f deployments/observability/compose.yaml \
  -f deployments/observability/compose.langfuse.yaml --profile langfuse config --quiet
docker compose -f deployments/observability/compose.yaml \
  -f deployments/observability/compose.langfuse.yaml --profile langfuse up -d
```

Open <http://127.0.0.1:3000>. The Collector uses `/api/public/otel/v1/traces` via OTLP/HTTP with Basic project-key authentication and `x-langfuse-ingestion-version: 4`. Langfuse does not support OTLP/gRPC. All platform spans still go to Tempo. Agent span attributes map to Langfuse via `langfuse.observation.type` (`agent`, `generation`, `tool`), `gen_ai.request.model`, `langfuse.release`, and `langfuse.trace.metadata.*`; propagate shared experiment/release/item context onto every Agent span for consistent filtering. Prompt and response capture is opt-in application behavior, not added by the Collector.

## Disposable local validation dependencies

`compose.langfuse-dev-storage.yaml` is an optional validation-only third file. It runs fresh PostgreSQL 16, Redis 7 and MinIO instances in dedicated `langfuse-dev-*` volumes, with only MinIO's browser endpoint published on loopback `19000`. It does not start Kafka or touch the application's data. The application storage template has development passwords, unrestricted published ports and an unsuitable public log-bucket initializer, so validation uses this isolated equivalent instead of altering it. This is **not evidence that the existing application storage instances were reused or tested**. The two-file regular Langfuse deployment above remains configured for existing externally supplied storage.

`scripts/observability/prepare_langfuse_dev.py --directory /path/to/private-runtime` creates random demo credentials and PostgreSQL bootstrap SQL as mode-600 files, refusing to overwrite existing files. These files must stay outside the repository/artifact exports. Supply its `langfuse-private.env` via Compose `--env-file` to all three configuration files. First start only `langfuse-dev-postgres`, `langfuse-dev-redis`, `langfuse-dev-minio`, and `clickhouse`; after PostgreSQL is healthy, pipe the generated SQL to `docker exec -i <postgres-container> psql -U postgres -d argus_dev -v ON_ERROR_STOP=1` to create the separate `langfuse` login/database. The private SQL file is never mounted into containers. Then start the full profile. The MinIO init service creates a private bucket and a separate account with the checked-in bucket-scoped policy. Redis uses `noeviction` with a 96-MiB ceiling, plus the independent Langfuse key prefix. PostgreSQL and Redis have no host ports.

ClickHouse is capped at 1.5 GiB (server memory target 1 GiB); local web at 1.5 GiB (1-GiB Node heap) and worker at 768 MiB. The ClickHouse XML bounds scheduler/thread pools while leaving enough threads for startup. The transient MinIO client init uses 256 MiB; 64 MiB was insufficient in actual testing. The web server also exceeded a 512-MiB Node heap during initial module loading, so its verified bound is higher. These are local validation bounds, not production capacity guidance. MinIO is digest-pinned to the public multiarch Chainguard image used by upstream Langfuse; legacy DockerHub MinIO tags were unavailable during validation.

## Import existing evaluation evidence

`scripts/benchmark/export_langfuse.py` consumes a directory of `scenario-*/*/outputs/argus_run.json`, a JSON row list, or a `rows`/`raw_results` artifact. It joins scenario/incident/case ID plus trial. Inputs from `score_entities.py` (`rows`) are named `strict.*`; the official judge's nested `scores` from `raw_results` are named `official.*`, without changing the upstream rubric. Nested official numeric calculations and intermediates remain score components; they must not be presented as independent headline quality KPIs. Report headline entity F1/reasoning metrics using the upstream aggregate definitions. `provider_stats`, latency, result and failures remain in the raw snapshot. Inputs require a case identifier and trial; ambiguous duplicates/conflicting trace IDs fail loudly. Missing scores are preserved as missing, never silently fabricated or filtered out.

```sh
python3 scripts/benchmark/export_langfuse.py \
  --runs /path/to/itbench-agent-runs \
  --strict-scores /path/to/entity-check.json \
  --official-scores /path/to/official-scores.json \
  --dataset itbench-local-revision --run experiment-2026-10-12 \
  --release git-commit --model model-name --out /tmp/langfuse-import.json
```

Default is dry run: a reviewable request plan contains source paths/SHA-256 receipts, every raw failure row, stable run/case IDs, and no credentials. Each case/trial produces an OTLP **import snapshot** evaluator root, with local experiment/dataset/item attributes. Its one-nanosecond span duration is an import marker, not measured execution latency. The actual `latency_ms` remains a separate score. Snapshot IDs derive from dataset/run/release/model/case/trial and remain stable for retries.

When an input row has real `trace_id`/`execution_trace_id`, the snapshot metadata links it and the same scores are attached to that original execution trace; optional `root_span_id` targets its root observation. The importer never invents or overwrites original runtime spans. Historical rows without trace IDs cannot retroactively recover execution traces. To inspect Agent model/tool/evidence calls, record real execution trace IDs at benchmark time and export those spans through the Collector. Experiment baggage/root metadata should be attached before execution if the actual Agent trace itself must be the Langfuse experiment item.

After reviewing the plan, explicitly set `LANGFUSE_BASE_URL=http://127.0.0.1:3000`, `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` and rerun with `--write`. This sends only to that explicit endpoint; remote HTTP is rejected, HTTPS is required outside loopback, redirects are rejected, and credentials come only from environment variables. Source artifacts can contain model output and incident details, so review the plan before writing to another server. Write receipts are saved alongside the plan as `.receipts.json`. All failed/partially rejected requests remain in the receipts and cause a nonzero exit; successful HTTP ingestion is transport acceptance, not UI/backend materialization proof. Stable score IDs permit deliberate reruns; no automatic retries or batch-207 retries are performed. The local HTTP tests make no paid model calls.

## Verified local ingestion evidence

On the isolated Colima VM, Langfuse `4.56.0` returned HTTP 200 from `/api/public/health`. The first two historical public benchmark rows produced 64 successful write requests, then authenticated API reads found 62 score components and two explicitly labeled import snapshot observations. The retained professional-run plan was subsequently imported in full: 957/957 requests accepted, with authenticated readback confirming 927 score components and 30 snapshot roots. Receipts and materialization checks are `outputs/langfuse-live-two-api-proof.json`, `outputs/langfuse-import-plan.json.receipts.json`, and `outputs/langfuse-import-api-proof.json` in the task artifact directory. These results verify local ingestion/readback; they neither create historical model/tool traces nor change evaluation quality. The 30 imported rows are benchmark executions across scenario/trial pairs, not a claim of 30 distinct scenarios.

A separately executed controlled Agent fixture produced a real execution trace `b91f8e554d70f73eac886e2b68feeaeb` through the Collector. Authenticated Langfuse readback found exactly ten observations: one `AGENT` root, three `GENERATION` model calls and six `TOOL` calls. The six actual tool names are `read_logs`, `query_metrics`, `query_service_logs`, `query_traces`, `error_statistics`, and `update_investigation_plan`. Langfuse v4's observation API exposes these under flat metadata key `attributes.tool.name`; request the `metadata` field group when inspecting them. A fixture completion score was attached to the actual root observation (`1f165256b328260b`) and independently to its import snapshot; Scores API subject readback confirmed both trace/observation targets. This fixture used a scripted model and synthetic signals, so the completion score is a transport/linking check, not diagnosis accuracy. Evidence: `outputs/langfuse-agent-observations-proof.json` and `outputs/langfuse-linked-agent-api-proof.json`.

Selective forwarding was also exercised: an unmarked backend probe span reached Tempo while Langfuse returned zero matching observations. See `outputs/langfuse-filter-proof.json`. This verifies the Agent-only pipeline on that controlled probe.

The conditional file trace parser was exercised with a real startup JSON log lacking both trace and span IDs; Collector → Loki ingestion succeeded and the receipt is `outputs/telemetry-startup-log-proof.json`. Run `scripts/observability/verify_langfuse.py --plan <plan.json> --out <receipt.json>` with the same explicit environment credentials to read health, score IDs and snapshot observations without saving authentication headers or full prompt/output payloads. `--execution-trace <real trace ID> --minimum-spans <N>` additionally verifies actual execution observations independently of import snapshots.

## Primary sources and image pins

Checked 2026-10-12. Images are version-pinned, not digest-locked; independently validate platform support and supply-chain policy before deployment. Collector `0.162.0`, Prometheus `v3.15.0`, Loki `3.7.8`, Grafana `13.2.3`, and Langfuse `4.56.0` were checked against official GitHub release APIs. Tempo stays on the verified `v2.9.0` release for its single-binary local configuration. ClickHouse `25.12` matches the Langfuse v4 upstream Compose example.

- [Langfuse native OTLP, filter and attribute mapping](https://langfuse.com/integrations/native/opentelemetry)
- [Langfuse experiment span attributes and score links](https://langfuse.com/integrations/native/opentelemetry/experiments)
- [Langfuse Scores API source schema, current POST `/api/public/scores`](https://github.com/langfuse/langfuse/blob/main/fern/apis/server/definition/scores.yml)
- [Langfuse v4 upstream Compose and dependencies](https://github.com/langfuse/langfuse/blob/main/docker-compose.yml)
- [Langfuse Redis prefix and ClickHouse environment schema](https://github.com/langfuse/langfuse/blob/main/packages/shared/src/env.ts)
- [Loki native OTLP endpoint and structured metadata](https://grafana.com/docs/loki/latest/send-data/otel/)
- [Collector filelog receiver operators](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/filelogreceiver)
- [Tempo v2.9.0 release](https://github.com/grafana/tempo/releases/tag/v2.9.0)

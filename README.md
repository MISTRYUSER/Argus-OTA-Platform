# Argus OTA Platform

Go event-driven vehicle log processing and a model-planned fault investigation Agent. Repository-root source is canonical; old snapshots and binaries remain only in Git history.

## Run locally

Go 1.24.12. `go mod download`, `make infra-up`, `make db-migrate`. Copy `.env.example` to ignored `.env.local`, set a private API-key file path (file permission 600), then `make run`. All eight service binaries are built and started; Ctrl-C stops them. Existing databases need migrations 04 and 05 as well as 03. Startup does not call a model until a diagnosis request is consumed.

Ingestor :8080; Query :8081; SSE :8090. Create batch `POST /api/v1/batches` with `vehicle_id`, `vin`, `expected_workers`; upload via `POST /api/v1/batches/:id/files`; complete via `POST /api/v1/batches/:id/complete`. Query `/api/v1/batches/:id/report` and `/progress`; stream `/api/v1/batches/:id/progress/stream`.

Parser and aggregator remain **explicitly mock**, using real uploaded file IDs and synthetic analysis. Real rec/C++/Python analysis remains a separate integration.

## Investigation Agent

Default `AI_RUNNER=investigation` uses an Eino Graph planning/execution loop. The model chooses read-only batch-scoped `read_logs`, `error_statistics` (affected-file counts), and `search_history`, then reads results and decides whether to continue. Reports cite actual tool-returned evidence IDs, store tool trace and limitations, and reject invented evidence. Maximum 8 planning calls, 4 tools per call, default 20 concurrent tasks and 2-minute task timeout. Confidence is model-reported and is not a calibrated quality score.

`AI_RUNNER=supervisor` retains the tested rule Supervisor; `conditional` retains the earlier confidence branch. DeepSeek chat works without an embedding API: case search uses structured/text lookup. Optional separate `EMBEDDING_API_KEY`, `EMBEDDING_BASE_URL`, `EMBEDDING_MODEL` enable vector retrieval.

## Reliability

All stages share `batch-events`, with separate consumer groups and explicit filters. Batch state and stage events commit together in PostgreSQL outbox; `event-relay` publishes them. Results and completion events commit together. Same-batch UUIDs normalize before locking; replay reuses saved results. Completion verifies result ownership and status.

Transient handler attempts cap at five; invalid task/model output and authentication failures stop immediately. Failure state persists before DLQ publication and offset acknowledgement. Subsequent quarantine retries never rerun the investigation. Full failed messages are retained in `dead_letter_messages`; Kafka DLQ contains a bounded reference and preview. Inspect failed batches and DLQ before manually resetting/replaying; terminal failed diagnoses do not auto-regenerate.

## Regression

`scripts/regression.sh stage-name` runs fresh full tests, vet and build. Set `ARGUS_TEST_DATABASE_URL`, `ARGUS_TEST_REDIS_ADDR`, `ARGUS_TEST_KAFKA_BROKERS` for real PostgreSQL/pgvector, Redis and Kafka tests. Integration tests use isolated database schemas and unique Kafka topics. Model and S3 fixtures stay local during normal regression.

Paid real DeepSeek smoke test is separate and opt-in: set `ARGUS_LIVE_DEEPSEEK=1`, `DEEPSEEK_API_KEY_FILE`, and the test database URL, then `go test -count=1 -run TestDeepSeekInvestigationLive -v ./workers/ai-agent/internal/application`.

**Diagnosis quality evaluation remains PENDING by user instruction.** No accuracy, latency improvement or production-readiness claims are inferred from functional tests.

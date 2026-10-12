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

## Agent harness upgrade

Research references: [Deep Agents graph architecture](https://github.com/langchain-ai/deepagents/blob/main/libs/deepagents/deepagents/graph.py), [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence), and [Eino checkpoint/interrupt guide](https://www.cloudwego.io/docs/eino/core_modules/chain_and_graph_orchestration/checkpoint_interrupt/). Concepts are adapted in Go/Eino; Python frameworks were not added. Recovery uses application-level model/tool-boundary checkpoints, not a claim of native Eino checkpoint integration.

The model can call `update_investigation_plan` to track up to five hypotheses with gathered support/refutation evidence and next checks. Reports select a plan hypothesis; refuted selections and unknown evidence are rejected. Planning/reference validation does not prove causal correctness; diagnosis quality evaluation remains PENDING.

Application checkpoints preserve model-call reservations, pending tool index, evidence, hypotheses, message state and completed report. A resumed run skips committed tool results and preserves its original step budget. A crash between an external API response and checkpoint commit may still repeat that call; exactly-once external model execution is not claimed.

Full tool outputs are retained in batch-scoped immutable artifacts. Older tool messages can compact to evidence summaries and artifact references; `read_evidence_artifact` reads bounded pages. Context has an explicit character budget while full trace/evidence remains available in durable storage.

Model execution runs outside long PostgreSQL transactions. Short claim/finalize transactions use renewable execution leases and generation fencing. Old owners cannot checkpoint, commit, or terminally fail a newer generation. Ownership contention does not consume model retry budget or publish false failure progress. Terminal failures commit before releasing ownership. Existing databases require migration `06-investigation-runtime.sql`.

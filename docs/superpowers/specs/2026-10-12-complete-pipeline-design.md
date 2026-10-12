# Argus pipeline completion

User scope: converge source, complete upload-to-query pipeline, reliable replay/recovery, and wire Supervisor. Diagnosis quality evaluation is PENDING. Run a full regression after EACH stage.

Canonical source is repository root. Adopt useful snapshot changes individually; remove tracked cache/snapshot/binaries while preserving them in Git history. Keep Go 1.24 and Eino 0.7.28. PostgreSQL owns durable batch progress, diagnosis results and an outbox; Redis provides caching and progress streaming. Publish durable events after committing state; retry dispatch and consumers without dropping failed offsets. Same batch result is reused on replay. All services share batch-events with distinct consumer groups and explicit event filters.

The legal state path is pending -> uploaded -> scattering -> scattered -> gathering -> gathered -> diagnosing -> completed. File completion is recorded by real file ID; only one aggregation request is emitted per batch. Mock parsing/aggregation remain explicitly mock. Reports join durable diagnosis data and cache only completed reports.

Supervisor uses Eino Graph loop edges and existing nodes, returns control after each worker, validates LLM JSON, gracefully degrades absent RAG, preserves failures, and bounds retries/steps. Rule-based decisions are retained; LLM-driven planning and quality/cost benchmarks remain deferred.

Regression: deterministic tests with an HTTP model fixture; real local PostgreSQL+pgvector and Redis integration tests, compilation/vet/race checks, an end-to-end workflow with observable persisted state and final query. No paid model call required. Kafka transport smoke test depends on locally available broker and must be reported separately.

## User-approved extension
A model-selected investigation Agent is the default runner, with read-only batch-scoped read_logs, error_statistics and search_history tools, evidence ID validation, stored tool trace and an 8-step budget. Rule Supervisor and conditional graph remain explicit runner options. Real DeepSeek functional tests are explicitly authorized with a private key file; quality evaluation remains PENDING.

Terminal investigation/output/authentication failures must be durably recorded and quarantined without restarting the model. Transient delivery attempts are capped at five; subsequent quarantine retries do not call the handler again.

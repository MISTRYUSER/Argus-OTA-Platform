# Argus Pipeline Implementation Plan

> For agentic workers: use superpowers:executing-plans, task by task.

Goal: Complete the four authorized stages with a regression gate after each.
Architecture: canonical root Go services, PostgreSQL durable state/outbox, Kafka event bus, Eino Supervisor.
Spec: ../specs/2026-10-12-complete-pipeline-design.md
Tech stack: Go 1.24, Eino 0.7.28, PostgreSQL/pgvector, Redis, Kafka, MinIO.

## Global Constraints
- Diagnosis quality evaluation stays PENDING.
- Full regression after each task; no completion claim without evidence.
- Preserve explicit mock parser/aggregator labeling.
- Work on codex/complete-diagnosis-pipeline in dedicated clone.

## Review Focus
- Cancellation must not cancel another query waiter.
- Duplicated/out-of-order completion cannot regress state or invent processed files.
- Broker/save failure cannot lose an accepted durable event.
- Missing RAG and malformed model output must not produce fake success.
- Consumer cancellation and bounded retries must not leak or spin.

### Task 1: Canonical source
Files: query_service.go and tests; snapshot SSE/config; .gitignore, go.sum, Makefile, README.
- [x] Run baseline.
- [x] Adopt query cancellation/error tests; observe failures.
- [x] Merge supported improvements and SSE; remove tracked snapshots/cache/binaries.
- [x] Full regression, build, commit.

### Task 2: Pipeline closure
Files: orchestrate_service, batch_service, postgres repositories, migrations, mock workers, AI consumer/service, report repository.
Interfaces: DiagnosisRunner.Run(ctx,batchID); durable batch repository update and outbox.
- [x] Tests: legal full state path, true file IDs, diagnosis persisted before completion, report exposes diagnosis.
- [x] Observe failing tests.
- [x] Implement unified batch-events and transactional durable state/results.
- [x] Full regression and real database integration; commit.

### Task 3: Reliability
Files: consumer loops, outbox dispatcher, durable deduplication and recovery; integration tests.
Interfaces: outbox publishes DomainEvent via KafkaEventPublisher; same-batch diagnosis reused.
- [x] Tests: duplicate delivery, publish failure/retry, rollback, malformed event, cancellation.
- [x] Observe failures then fix.
- [x] Full regression and race checks; commit.

### Task 4: Supervisor
Files: application/supervisor, domain/state, AI entry/config and tests.
Interfaces: DiagnosisRunner implemented by SupervisorGraph and DiagnosisGraph.
- [x] Tests: fast route, slow route, unavailable RAG, low confidence retry, failure and termination bound.
- [x] Observe failures then reuse tested nodes in loop graph and wire production entry.
- [x] Full regression and end-to-end flow; commit.

### Pending
- [ ] Diagnosis quality evaluation (explicit user deferral).

## Final validation
48 functional tests passed, 0 failed; 1 paid live test excluded from normal regression and executed separately successfully. Full race regression, vet, whole-module build and make build passed. Real PG/Redis/Kafka end-to-end path and real DeepSeek tool investigation verified.

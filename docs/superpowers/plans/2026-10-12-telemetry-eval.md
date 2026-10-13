# Telemetry/eval implementation plan

Spec: docs/superpowers/specs/2026-10-12-telemetry-eval.md
Execution: root implements observability and integration; independent parallel tasks own evidence clients and middleware/eval config. Review whole branch at end.

- [x] Implement OTel instrumentation and structured logs with export/propagation tests; stage regression.
- [x] Implement trusted-scope Prometheus/Loki/Tempo adapters + immutable scope storage and tests.
- [x] Bind optional telemetry tools to Agent/worker, durable query limits and checkpoint policy; full regression.
- [x] Add reusable LGTM/Collector/Grafana/Langfuse deployment and score ingestion; validate configuration and local integration.
- [x] Run end-to-end evidence+execution-trace smoke, full/race regression, independent review, save evidence and draft PR.

## Verified execution receipts

Full telemetry-final regression: 120 tests passed, 4 opt-in tests skipped; vet/build passed. Separate real middleware Agent test passed with 10 correlated spans and all three scoped evidence tools. Targeted race: 66 passed, 2 opt-in skipped. Static middleware checks: 9 passed; importer protocol: 6 passed. Actual native middleware and local Langfuse health/write/API readback verified. Reports and raw receipts are delivered in the task outputs directory; no credentials are stored in Git.

Telemetry read budget counts committed observations (maximum six). A crash after a backend read but before its checkpoint can repeat that read; this does not promise exactly-once physical reads. Missing/out-of-scope telemetry remains an explicit evidence limitation.

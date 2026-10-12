# Agent scenarios and measurement

Goal: ship three reproducible stories with regression evidence and explicitly scoped measurements, never represent synthetic/local results as production scale.

1. Investigation reversal: query-sensitive history retrieval; model reads contradictory current logs, refutes H1 and supports H2. Bound repeated searches and retain full tool/plan history. Verify with scripted protocol tests and independent real-provider synthetic cases. Model outcomes are scored against fixture truth, not self-reported confidence.
2. Recovery: subprocess exits after committed tool checkpoint; a new process restores pending cursor, step/usage/reservation counters and produces final result without replaying completed tools. Existing PostgreSQL fencing tests verify stale-owner writes. External response/checkpoint gap remains explicitly non-exactly-once.
3. Capacity: process-local model admission bounds active plus waiting requests, context-aware bounded 429 retry, cancellation propagation, per-investigation reservation budget saved before dispatch. Shared-provider/multi-replica quotas are outside this local gate. No default streaming bypass.
4. Evaluation: versioned synthetic cases and explicit deterministic/live/load categories; aggregate raw rows, pass criteria, percentile latency, token usage and provenance. Local scale uses controlled provider responses, not paid provider flooding. Real-provider sample is small and capped; no production generalization.

Non-goals: production SLA claims, fake business improvement percentages, autonomous vehicle repair, bulk paid API pressure testing, real private vehicle data. Existing parser/aggregation remain mock; disclosed in report.

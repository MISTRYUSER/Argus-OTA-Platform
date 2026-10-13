# Full benchmark measurement, 2026-10-13

The complete pinned ITBench-Lite SRE v0.2 snapshot contains 35 scenarios. Agent and single-shot baseline each ran three trials per scenario: 210 logical trials. A prespecified replay selected only 98 terminal provider 402/429 failures, retaining all 308 physical trial attempts. Failed replays remain in the final denominator; invalid-JSON predictions were not selectively rerun.

| Measurement | Agent | Baseline |
| --- | ---: | ---: |
| Valid report contract | 101/105 (96.19%) | 102/105 (97.14%) |
| Upstream non-Top-K root entity F1, 35-incident macro | 0.150476 | 0.313076 |
| Defined raw non-Top-K F1 values | 103/105 | 105/105 |
| Upstream reasoning, 35-incident macro | 0.20 | 0.50 |
| Strict top-1 root match, lower bound over all tasks | 2/105 | 5/105 |
| Strict score coverage | 102/105 | 102/105 |
| Tasks with recorded tool observations | 105/105 | 0/105 |
| Recorded nonzero execution trace ID | 105/105 | 105/105 |

Valid report rate measures completion/schema/evidence requirements, not correct diagnosis. Tool use is partly required by the contract and is not a quality proxy. The Agent is currently below the baseline on diagnosis quality.

The official macro follows the upstream incident-first aggregation, omitting undefined trial scores within each incident. Undefined values are retained, not filled with zero. Scenario 38 has an invalid Python regex (`*.*`) in the original ground truth: strict scores for its three trials are explicitly unknown. Original gold and the official rubric are unchanged. Secondary upstream Top-K recall/F1 contain 96 out-of-range component values; raw outputs remain available, but those components are excluded from validated headline KPIs.

The label-blind ITBench adapter includes alerting-state alerts, Warning events and latest otel-demo Kubernetes objects. It omits metrics, application logs and traces. The baseline reads the first 24,000 characters; the Agent can page/filter adapted records under its context/model budgets. No historical retrieval database is supplied to this transfer runner. Diagnosis and official judging use the requested DeepSeek alias; same-model judging is not independent human validation. Previously inspected 15 scenarios and 20 new scenarios are separately identifiable.

## Retrieval component

The complete BEIR SciFact retrieval test has 300 qrel-defined queries, 5,183 documents and 339 positive relevance pairs. The production PostgreSQL text fallback was called through a session-local temporary table; reconnects cannot fall back to business tables.

| Measurement | Original production fallback | Repaired production fallback | Offline BM25 comparison |
| --- | ---: | ---: | ---: |
| Recall@10 | 0 | 0.632222 | 0.774000 |
| Precision@1 | 0 | 0.403333 | See raw scores |
| Precision@10 | 0 | 0.069333 | 0.085000 |
| nDCG@10 | 0 | 0.514682 | 0.651893 |
| Mean query latency, ms | 26.65 | 811.98 | See raw scores |
| P95 query latency, ms | 30.47 | 1106.71 | See raw scores |

Source investigation showed a full-query ILIKE gate rejected distributed matching terms. One prespecified repair uses PostgreSQL simple lexeme OR matching, literal phrase priority and ts_rank. Exact error-code filtering/top-K cap remain intact; wildcard characters are escaped. Independent fixtures were written before the repair. No parameters were chosen from fixed test scores; this is a post-test defect repair, not an untouched zero-shot result.

English retrieval component results are not OTA-domain RAG accuracy. BM25 remains an offline comparison. The repaired unindexed temporary corpus adds substantial query cost; no production throughput gain is claimed. Precision@K uses denominator K, not the number of returned relevant documents.

## Engineering and evidence

OTel exports HTTP/Kafka/task/model/tool spans and bounded-label metrics; JSON logs correlate trace IDs. Trusted namespace/service/time bindings constrain Prometheus/Loki/Tempo tools. Six committed telemetry observations are allowed; a crash before checkpointing can repeat a read. Response normalization is capped at 16KiB, and six outputs fit a completed checkpoint. Checkpoints reject changed telemetry policies.

Local native tests verified all three telemetry signals, ten correlated Agent spans, Langfuse score linkage and non-Agent trace filtering. Independent process identities prevent cumulative metric collisions; two real SDK counters 3/7 remain separate, sum10. Duration histograms use explicit second-scale buckets for subsecond tools and long model tasks.

Raw benchmark records, immutable input/source hashes, per-query relevance scores, SQL plans, failed runs and archive manifests are delivered as task artifacts. Two first-run baseline parse failures did not preserve their raw replies under the old recorder; error/status/token/trace records remain, and later recorder code preserves invalid replies. Historical counters/buckets preceding instrumentation repairs are not benchmark totals or latency measurements; benchmark metrics use raw trial records.

This remains a local development/test deployment. Parser/aggregator components are mock, and production fleet scale, HA and online business gains have not been measured.

Primary sources: [ITBench-Lite](https://huggingface.co/datasets/ibm-research/ITBench-Lite), [ITBench Evaluations](https://github.com/itbench-hub/ITBench-Evaluations), [BEIR evaluation protocol](https://github.com/beir-cellar/beir/blob/main/beir/retrieval/evaluation.py), [SciFact licensing](https://github.com/allenai/scifact/blob/master/LICENSE.md), [OTel service instance semantics](https://opentelemetry.io/docs/specs/semconv/registry/attributes/service/).

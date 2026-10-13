# Reproducible Agent evaluation

## Professional dataset
IBM ITBench-Lite SRE, Apache-2.0, dataset revision `d0916b08ba421ce5e672e9ad68aa947d938dfef0`, snapshot version `v0.2-B96DF826-4BB2-4B62-97AB-6D84254C53D7`.
Primary source: https://huggingface.co/datasets/ibm-research/ITBench-Lite
Official evaluator: https://github.com/itbench-hub/ITBench-Evaluations, revision `14f026fc9cc348c4ecec5ab32714de954c95c1b1`.

Development scenarios: numeric IDs 1,2,4,5,6,7,8,9,11,12 selected before reading labels; three trials each. Held-out scenarios: 13,14,15,16,17 selected before their predictions and labels; one trial each. Keep all failed versioned runs. Never overwrite previous output folders.

This is an adapted offline SRE transfer pilot, not an official leaderboard submission or proof of automotive diagnostic quality. Inputs contain alerting snapshots, warning events and latest otel-demo Kubernetes objects; metrics, application logs and traces are omitted. The label-blind adapter strips bulky metadata, preserves adapted records in addressable chunks, records input hashes, and never reads ground_truth.yaml. Ground truth is only passed to the scorer after prediction. Objects are selected by timestamp, not label relevance.

Run downloader and adapter with Python 3.10+ (adapter uses stdlib only):

```
python scripts/benchmark/download_itbench.py --out <dataset-directory>
python scripts/benchmark/prepare_itbench.py --data <dataset-directory> --out <blind-inputs.json>
go run ./workers/ai-agent/cmd/agent-bench -input <blind-inputs.json> -out <new-agent-results> -trials 3 -parallel 2
```

Set DEEPSEEK_API_KEY_FILE to an existing private key file. Agent trials use production Eino investigation logic, max12 planner calls, 24000 stored-context characters, and 1000000 conservative reservation units. Task instructions are persisted and counted. Reservoir units are not tokens billed by a provider; actual usage is recorded separately. Failure checkpoints and request tool policy (names/choice only; no headers) are retained.

The single-shot baseline uses the same adapted input prefix (24000 characters), model and final schema, without tool access. It is an architectural comparison, not a controlled prompt-only ablation: the Agent can selectively retrieve more of the same snapshot. Successful structured output is distinct from correct root-cause identification.

Official evaluator rubric and aggregation are unmodified. `scripts/benchmark/run_itbench_judge.py` adds only DeepSeek compatibility settings (thinking disabled, completion limit8192); evaluator uses DeepSeek Flash in separate judge calls. Same-model judging can be biased; report it, include raw scores and deterministic entity checks, and do not call it independent human review. Denominators must include failures; preserve upstream denominators/undefined metrics explicitly.

## Operational tests

- Actual subprocess exits with code77 after a committed tool checkpoint, resumes in a new process, reuses the saved tool prefix and completed result, and preserves usage/step/reservation counters.
- PostgreSQL integration tests verify generation fences reject old-owner checkpoints/finalization; temporary local infrastructure is not a production deployment.
- Controlled HTTP load: 32clients, three profiles x1000 logical requests, 5ms provider latency and every seventh provider attempt returning429. Report retry overhead, success, p50/p95/p99 and peak concurrency. Reported fixture tokens are synthetic and not billable. These are model-gateway requests, not end-to-end OTA diagnosis throughput.

Ordinary regression: `GO_BIN=go scripts/regression.sh <stage>` with PostgreSQL/Redis/Kafka test variables. Paid and local load tests are opt-in; `ARGUS_LOAD_EVAL=1 ARGUS_LOAD_REPORT_DIR=<dir> go test ./workers/ai-agent/internal/infrastructure/llm -run TestGovernorMeasuredLoad`.

## Deployment and limits

Checkpoint policy is now argus-harness-v3. Drain pending v1/v2 investigations under the old worker before deploying; do not silently reinterpret old checkpoints. Completed persisted diagnoses remain readable. Do not delete snapshots as part of rollout without an explicit recovery plan.

LLM_CONCURRENCY (default4) and LLM_QUEUE_CAPACITY (default32) govern local admission. Migration09 and default-enabled LLM_SHARED_QUOTA additionally coordinate model attempts across replicas in the same explicit PostgreSQL scope; see [the current processing guide](version-regression-guide.md). AI_MODEL_RESERVATION_BUDGET defaults1000000. Cancellation reaches queue waits, backoff and HTTP calls. Queue-full admission becomes task deferral and does not consume investigation budget. A provider response followed by a crash before checkpoint persistence can repeat an external call; exactly-once billing is not claimed.

Real CSV/Apollo parsing and Python aggregation are now default; mocks require ARGUS_PROCESSING_MODE=mock. Historical 2026-10-12 reports keep their original mock disclosure. Benchmark accuracy, operational correctness, local load scale, and production business results are separate evidence categories. No production SLA or enterprise deployment claim follows from these tests.


## 2026-10-13 extension

[Processing/routing/shared quota guide](version-regression-guide.md) and [measurement report](version-regression-results.md) distinguish real record decoding, synthetic comparison checks, HTTP routing fixtures and real subprocess/database recovery. ITBench model quality does not automatically transfer to OTA.

## 同领域故障现场回放

[OTA 实际 HTTP 故障包与三策略评测](ota-fault-replay.md)：12 个实际运行故障，固定分支命中 2/12；真实模型对照尚未运行。复用 agent-bench，不新增评测平台。

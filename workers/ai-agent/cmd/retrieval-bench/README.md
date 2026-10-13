# Retrieval component benchmark

This command measures the actual `pgvector.NewTextRetriever(db).Search` fallback on the complete **BEIR SciFact test split**. It does not modify the production retriever, call embedding/model APIs, or measure OTA business RAG accuracy. SciFact is an English scientific-claim retrieval dataset: 5,183 corpus documents, 300 judged test queries, and 339 positive relevance judgments. `queries.jsonl` contains 1,109 queries across splits; the runner evaluates only the exact IDs in `qrels/test.tsv`.

The benchmark connects to a local PostgreSQL instance using a connector-enforced `search_path=pg_temp`, `MaxOpenConns=1`, and `MaxIdleConns=1`. It creates a session temporary `knowledge_base` and loads title/abstract into symptom/solution columns. Existing business tables are neither read nor written, and a reconnect cannot fall through to a public table. Disconnect destroys the temporary corpus. Corpus document IDs remain strings; row `i+1` maps through the same UUID SHA1 namespace calculation as the production retriever (`knowledge-base:<row-id>`).

Each output query retains ranked corpus IDs, returned diagnosis UUIDs, actual production similarity, latency, empty results, and errors. The text fallback's production similarity is zero; a separately labeled rank-derived score preserves its returned order for TREC evaluation. Failures contribute zero to quality metrics and are counted separately. Missing test queries or duplicate output IDs are rejected instead of silently reducing the denominator.

Evaluation uses `pytrec-eval-terrier` / `pytrec_eval` for macro Recall, Precision, nDCG, MAP, and truncated reciprocal rank at 1, 3, 5, and 10. Truncated reciprocal rank is equivalent to BEIR's MRR@k. Precision divides by k even when fewer records are returned. Empty/error rows count in the full 300-query denominator. BEIR's default identical query/document ID exclusion is retained. The scorer records its version, input hashes, and per-query scores. The downloader verifies the official archive MD5, saves SHA256 for every raw file, pins the dataset card revision, confirms immutable official Hugging Face qrels against the archive, and saves the pinned official BEIR evaluator source for inspection.

An optional, explicitly separate **offline BM25 baseline** uses `rank-bm25` default BM25Okapi parameters, lowercased Unicode word tokens, and title plus abstract. It calls no production code and is not presented as an implemented improvement. Its real BM25 scores are retained; separately labeled rank-derived scores preserve the reported deterministic corpus-order tie break for evaluation.

Run from the repository root (the development runtime and disposable local PostgreSQL must be running). **Always choose a fresh output directory.** The historical `rag-beir-scifact-current` directory records the old SQL behavior and must not be rerun using today's source. Reproducing that zero baseline requires its archived source snapshots. Raw output, score output, per-query score output and SQL plans use exclusive creation and refuse existing files without changing them. The command's default output is a new UTC timestamp directory.

The following example intentionally uses a new directory; change its suffix for every run:

```sh
../benchmarks/venv/bin/python -m pip install pytrec-eval-terrier==0.5.10 rank-bm25==0.2.2
../benchmarks/venv/bin/python scripts/benchmark/beir_scifact.py download --dest ../benchmarks/beir-scifact
../run-go.sh run ./workers/ai-agent/cmd/retrieval-bench -data ../benchmarks/beir-scifact/scifact -out ../../outputs/rag-beir-scifact-rerun-new/production.jsonl
../benchmarks/venv/bin/python scripts/benchmark/beir_scifact.py score --qrels ../benchmarks/beir-scifact/scifact/qrels/test.tsv --run ../../outputs/rag-beir-scifact-rerun-new/production.jsonl --out ../../outputs/rag-beir-scifact-rerun-new/production-metrics.json
../benchmarks/venv/bin/python scripts/benchmark/beir_scifact.py baseline --data ../benchmarks/beir-scifact/scifact --out ../../outputs/rag-beir-scifact-rerun-new/bm25-offline.jsonl
../benchmarks/venv/bin/python scripts/benchmark/beir_scifact.py score --qrels ../benchmarks/beir-scifact/scifact/qrels/test.tsv --run ../../outputs/rag-beir-scifact-rerun-new/bm25-offline.jsonl --out ../../outputs/rag-beir-scifact-rerun-new/bm25-offline-metrics.json
```

For the 2026-10-13 run, the production fallback returned no records for all 300 test queries, with **zero query failures**. Recall/Precision/nDCG/MRR at all four cutoffs are therefore **0**. The SQL first requires the complete query sentence to appear as a substring of one title or abstract; this broad scientific retrieval test shows that this gate excludes relevant documents. A separate fixture proves that a matching substring does return and map the expected document, so the empty run is not a broken corpus load or identity mapping. This finding describes the tested text-fallback behavior, not all possible structured error-code or vector-embedding paths.

Official sources: [BEIR dataset card](https://huggingface.co/datasets/BeIR/scifact), [test qrels](https://huggingface.co/datasets/BeIR/scifact-qrels), [BEIR evaluator](https://github.com/beir-cellar/beir/blob/main/beir/retrieval/evaluation.py), [BEIR custom metrics](https://github.com/beir-cellar/beir/blob/main/beir/retrieval/custom_metrics.py). Raw data and its source manifest are under `work/benchmarks/beir-scifact`; current output and per-query metrics are under workspace `outputs/rag-beir-scifact-current`.

## Minimal lexical fix and complete rerun

After the zero-return baseline, one strategy was declared and fixed before rerunning: parse with `simple` full-text lexemes, replace only parser-generated AND operators with OR, prioritize escaped literal phrase matches, then sort by `ts_rank`. Structured error-code filtering and TopK ≤10 are unchanged; an unstructured empty lexical query cannot return all documents. Independent invented fixtures for dispersed terms, phrase priority, literal backslashes, punctuation, structured filtering and limits failed before the relevant fix and pass afterward.

The same complete 300-query rerun is retained separately in `outputs/rag-beir-scifact-fixed`: Recall@10 **0.632222**, Precision@10 **0.069333**, nDCG@10 **0.514682**, MRR@10 **0.487806**, with zero query failures. This restores recall relative to the original zero-return fallback but remains below offline BM25 (Recall@10 **0.774000**, Precision@10 **0.085000**). Mean local query latency increased from **26.65 ms to 811.98 ms (30.47 times)**, and p95 from **30.47 ms to 1106.71 ms**. Full-text vectors are computed on the fly over the temporary corpus without a GIN index. No production performance or OTA RAG accuracy improvement is claimed, and no repeated qrel-driven tuning was performed.

To record the actual production query plan without duplicating its SQL or rerunning all queries:

```sh
../run-go.sh run ./workers/ai-agent/cmd/retrieval-bench -data ../benchmarks/beir-scifact/scifact -plan-only -plan-out ../../outputs/rag-beir-scifact-plan-new/production-sql-plan.json
```

The command reads the production SQL constant through Go's AST and executes EXPLAIN ANALYZE only against the isolated session corpus. Per-query latency scoring excludes missing values and reports their count; NaN, infinite, or negative values are rejected.

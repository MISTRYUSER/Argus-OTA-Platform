# Complete benchmark measurement plan

User request: after telemetry integration, run the complete benchmark set and record retrieval recall/precision, task success, invocation rates, implementation and evidence.

## Fixed scope and definitions

- ITBench-Lite SRE pinned dataset revision d0916b08ba421ce5e672e9ad68aa947d938dfef0, v0.2 snapshot: all 35 scenario directories, discovered from the upstream nonrecursive index. Run Agent and single-shot baseline, three trials per scenario (210 total trials). Preserve failures, context policy, input hashes, model identity, code revision and HTTP attempts/statuses. Existing 15 scenarios are previously inspected; remaining 20 are new scenarios, reported separately. The adapter remains alerting alerts + Warning Kubernetes events + latest otel-demo objects; this is the complete scenario set under that adaptation, not a full-modalities or live ITBench leaderboard run.
- Diagnosis validity (schema/evidence contract) is distinct from correct root-cause task success. Score labels only after model execution. Use upstream official rubric and strict namespace/kind/entity matching, with explicit undefined-score coverage. Historical bad results are retained.
- Tool invocation rate: tasks with tool calls / all tasks; per-tool task use, attempt count, errors, unavailable responses and nonempty evidence rates. Model HTTP attempt status captures actual429; missing historical statuses stay unknown. Include latency distribution, token usage, model steps, trace coverage and failures.
- Retrieval: BEIR SciFact entire test split (300 qrel-defined queries over5183 documents) using the actual production PostgreSQL text fallback. Session-local temporary knowledge_base avoids mutating business data. Official qrels and BEIR/pytrec_eval conventions for Recall/Precision@1,3,5,10, nDCG/MRR. This measures a general English retrieval component; it is not OTA-domain RAG accuracy and does not prove dense embeddings or business diagnosis performance. No tuning on test labels.

## Tasks and regression gates

- [x] Freeze telemetry code, complete native middleware/eval evidence and regressions.
- [x] Download and hash all35 declared inputs; prepare label-blind records; run210 production model trials with realDeepSeek and OTLP export.
- [x] Add/test full-trial summarizer including failure/unknown/duplicate handling; score all diagnosis trials.
- [x] Add/test retrieval benchmark against realPostgreSQL; run all300queries and save official scoring receipts.
- [x] Independently review metric definitions/data completeness; run fullGo/Python regression after changes.
- [x] Deliver raw data, measurement report, source revision and STAR text supported by actual results; create reviewable PR.

## Review focus

No label leakage into diagnosis inputs; no test-qrel tuning; failed tasks stay in denominator; no invented429/unknown scores; no business-table mutation; no credentials in raw archives; code revision matches evaluated tree; synthetic reliability fixtures and full professional quality benchmarks remain separately labeled.

## Prespecified retrieval defect repair

The first complete300-query run returned no documents for any query. Source investigation showed the production fallback required a literal full query substring before its full-text rank could run. Preserve that baseline. Before evaluating any fixed result, freeze one general repair: simple PostgreSQL lexeme OR matching, literal phrase match priority, then ts_rank; structured error-code filtering remains exact, topK remains capped10, and empty/punctuation-only unstructured queries return no rows. Verify on independently authored distributed-term/phrase/structured/escaping/topK fixtures. Run identical complete300queries once into a new fixed directory without choosing parameters from test qrels. This is post-test defect repair, not an untouched zero-shot score; retain old/fixed/offlinebaseline distinctions and source hashes.

Final source verification: all-final-verified 138 pass/5 opt-in skips; vet/build passed. Python36 and configuration9 passed. Real native telemetry receipts retained; environment reset removed disposable VM, so persistent Lima state and middleware restoration were added. Original210 attempts plus98 explicitly scheduled provider replays are all retained. Report/STAR/data archive and PR publication are task outputs, with independent reviewed provenance.

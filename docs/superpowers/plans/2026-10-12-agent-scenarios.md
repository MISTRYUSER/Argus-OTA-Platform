# Agent scenarios implementation plan

Goal: deliver hypothesis reversal, subprocess recovery and rate-limit governance with reproducible evaluation results.
Architecture: keep Eino investigation graph and PostgreSQL fence; enhance retrieval and add provider admission wrapper. Test all paths through actual runner/provider interfaces. Collect structured raw results, then generate a scoped report.
Spec: docs/superpowers/specs/2026-10-12-agent-scenarios.md
Constraints: synthetic evidence labeled; paid API opt-in; no secrets in outputs; regression after each stage; no production-scale claim.

- [x] Stage 1: query-sensitive text ranking; at most three distinct history searches, reject duplicates; deterministic reversal test retaining initial and revised plans.
- [x] Stage 2: persist reserved model budget; subprocess termination/restart test proving completed tool prefix and usage/step reservation reuse; existing real DB fencing regression.
- [x] Stage 3: bounded model admission, 429 retry and cancellation; worker config; concurrent load, queue rejection, retry exhaustion and cancellation tests.
- [x] Stage 4: versioned evaluation manifest, real DeepSeek synthetic reversal cases, load rows and summary with provenance, independent final review, full/race regression and build.

Review focus: checkpoint policy changes; waiting cancellation leaks; retries exhausting reservation; misleading quality metrics; fixture-specific query behavior; subprocess checkpoint corruption and stale DB writes.

Outcome: mechanisms verified; professional RCA scores below baseline, disclosed in results. Do not claim production readiness or diagnostic accuracy improvement.

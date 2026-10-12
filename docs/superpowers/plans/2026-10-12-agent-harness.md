# Agent harness implementation plan

> Execute inline with superpowers:executing-plans; one final independent review.

Goal: Add evidence-backed planning, resumable state/context artifacts and fenced execution leases.
Spec: ../specs/2026-10-12-agent-harness.md
Constraints: Go 1.24/Eino 0.7.28 retained, no quality evaluation, regression after every stage, no secrets in Git.

### Task 1: Planning
- [x] Write plan validation tests and observe RED.
- [x] Add bounded hypotheses/plan tool and persisted report field; verify valid and invented evidence cases.
- [x] Fresh full regression, vet/build; commit.

### Task 2: Checkpoints and context
- [x] RED tests: crash after successful tools resumes without replay; pending tool index; completed report reused; policy/model/batch mismatch rejected; cumulative budget not reset.
- [x] Add versioned application checkpoint store and batch-scoped artifact store with PostgreSQL implementation; compact long tool messages with evidence references.
- [x] Fresh regression and real PG checkpoint tests; commit.

### Task 3: Leases and integration
- [x] RED tests: model executes outside transaction, same-batch concurrency, expired owner rejection, failure recording cannot clobber active task.
- [x] Add short claim/finalize, heartbeat, fencing context and busy-task deferral.
- [x] Regression/race/live smoke; independent review, fix important findings; draft PR.

Pending: diagnosis quality evaluation; native rec parser; multi-agent delegation and human approval UI are not part of this scope.

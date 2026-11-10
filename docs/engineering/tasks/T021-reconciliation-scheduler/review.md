# Review — T021 (2026-11-10, round 1)
Verdict: Approved
Summary: The scheduler does what TDR-009 §1 describes:
- a per-job goroutine with jittered, non-overlapping ticks;
- a session advisory lock taken and released on the same connection, and the connection closed if the unlock fails, so a lock can never leak back into the pool;
- `job_runs` rows for real runs only, as decided;
- recording that still happens during shutdown.

The CLI and the `serve` wiring are small and tested. One Nit.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/reconciliation/scheduler.go:286` | Plan conformance | The task named `lock.go` / `store.go` and a `reconciliation-deps` depguard rule. The code sits in one file, and the rule is deferred to T022 because the module imports no business package yet. Both are documented. | Add the rule with T022, together with the first business imports. |

## Checked and fine
- **A Architecture:**
  - `reconciliation` owns `job_runs` and imports only platform.
  - No HTTP types.
  - The `reconJobs` hook in `cmd/wallet` keeps job wiring in the composition root.
- **Correctness:**
  - `pg_try_advisory_lock` and `pg_advisory_unlock` run on the same `pgxpool.Conn`.
  - Each job has its own timeout; a panic is recovered and recorded as FAILED.
  - `Run` cannot be called twice, and `Register` is guarded after `Run`.
  - Ticks of one job are serialized (the wait starts after the run).
  - Shutdown waits for the running jobs.
- **Go:**
  - Errors are wrapped; doc comments explain why.
  - The `gosec` nolint on the hash-to-int64 conversion is justified.
  - gofmt, vet and lint report 0 issues.
- **Data:**
  - The migration matches the data model.
  - `SKIPPED` is allowed but unused, as decided; there is an index for per-job history.
- **Tests:**
  - Every AC is mapped; the two-instance test proves a single runner.
  - The lock vectors were computed independently.
  - The serve test is made robust for `-race`.
  - Coverage: 94%.

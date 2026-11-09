# Review — T012

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The outbox matches TDR-006 and the task:
- enqueue runs in the caller's transaction;
- the claim uses SKIP LOCKED with a lease;
- every mark is guarded by `status = 'PROCESSING' AND attempts = job.Attempts`, so a stale mark changes nothing;
- `Fail` ends as DONE with the reason;
- DEAD comes after `OUTBOX_MAX_ATTEMPTS`;
- the gauges and the `OUTBOX_ENABLED` role flag work.

Each AC has a test, and the concurrency ACs (two runners, an expired lease, wake-up) use real
runners against PostgreSQL. The review found no Blocker or Major issues: one Minor (accepted
and documented) and two Nits.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor (accepted) | `internal/outbox/store.go` (`Claim`, marks) | TDR-006 §2 SQL uses `now()` | Lease and due times come from each instance's `clock.Clock`, not the database's `now()`. With clock skew between instances larger than the lease (60 s), one instance could take over a lease that is still valid. This only causes an extra delivery, which is allowed (at-least-once, idempotent handlers). | Keep: the app clock is the project convention (deterministic tests, the same time source as `created_at`). NTP keeps skew far below 60 s. Recorded under deviations in `implementation.md`. |
| 2 | Nit | `internal/outbox/service.go` (`Enqueue`) | Fail fast | An empty `AggregateType` or a nil `AggregateID` is accepted. | Optional: validate both. Today every caller (T017/T018) passes the transaction. |
| 3 | Nit | `internal/platform/metrics/metrics.go` | Prometheus naming | `outbox_pending_total` is a gauge, but `_total` is the counter suffix by convention. | Keep: the name is fixed by TDR-006 §6 and the task. |

### Checked and fine
- **A Architecture:**
  - `outbox` imports only `platform/{clock,id,queue}` and its own sqlcgen (`go list`).
  - The depguard rule `outbox-deps` is probed: importing `wallet` fails lint.
  - The table is owned by the module, and `cmd/wallet` is the only place it is wired.
- **B Correctness:**
  - The event is inserted in the caller's tx (a rollback leaves no row, tested).
  - Claim SQL as in TDR-006 §2. `MarkDone` clears `last_error` from earlier retries, so `last_error` on a DONE row always means a `Fail` reason.
  - The dead counter counts only a real (non-stale) DEAD mark.
  - An unknown type at delivery is retried, not dropped.
  - `Wake` never blocks.
- **C Go:**
  - `Register` panics on wiring bugs (duplicate, nil, after `Run`), and `Run` twice is an error.
  - Goroutines end with ctx: the backlog loop is waited for.
  - Errors are wrapped; the int32 conversions are clamped (gosec clean).
- **D Data:**
  - The migration matches the task (CHECK, partial index, aggregate index), with a down migration.
  - sqlc casts keep the types non-nullable.
  - Config defaults and validation are as listed, and every invalid value names its variable.
- **E Tests:**
  - AC-1…9 are mapped in `implementation.md`; the integration tests are stable over 5 `-race` runs.
  - Package coverage is 95.5%, and the `serve` role flag is tested end to end.

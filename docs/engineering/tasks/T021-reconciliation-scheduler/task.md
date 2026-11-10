# T021 — Reconciliation scheduler (job runner, `job_runs`, CLI)

| Field        | Value |
|--------------|-------|
| ID           | T021 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T021-reconciliation-scheduler` |
| Depends on   | T002 (database), T003 (metrics) — phase 1 done |
| PRDs         | PRD-002 §7, PRD-003 §7 (the jobs that will run on it) |
| TDRs         | TDR-009 §1, TDR-001, TDR-012 |
| Docs changed | `docs/engineering/roadmap.md` (phase-2 split into T021–T023) |

## What
The empty machinery for reconciliation: a module `internal/reconciliation` with a scheduler that
runs registered jobs on an interval, one instance at a time (Postgres advisory lock), records
each run in `job_runs`, and exposes metrics. `wallet reconcile --job=<name>` runs one job once
from the command line. No business job is registered yet; `pending_bank_ops` comes in T022 and
`dead_outbox` / `stuck_alert` in T023.

Deliverables:
- Migration `000010_job_runs`.
- `internal/reconciliation`: `Job`, `Scheduler` (`Register`, `Run(ctx)`, `RunOnce(ctx, name)`).
- Config `RECON_ENABLED` (default `true`).
- `cmd/wallet`: the scheduler runs inside `serve` when enabled; new command `wallet reconcile --job=<name>`.
- Metrics `recon_job_runs_total{job,status}`, `recon_job_duration_seconds{job}`, `recon_items_total{job,kind}`.

## Why
TDR-009 §1 (in-process scheduler, advisory lock, `job_runs`, `RECON_ENABLED`, a `wallet reconcile
--job=X` sub-command calling the same functions); TDR-001 (one binary, roles by flags). Split from
roadmap P2-2/P2-3 into three tasks (decided 2026-11-10).

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000010_job_runs.up/down.sql` | Table below |
| `internal/reconciliation/scheduler.go` | `Job`, `Result`, `Scheduler`, `Register`, `Run`, `RunOnce` |
| `internal/reconciliation/store.go`, `queries.sql` (+ `sqlc.yaml` block) | Insert a `job_runs` row |
| `internal/reconciliation/lock.go` | `pg_try_advisory_lock` / `pg_advisory_unlock` on one pooled connection |
| `internal/platform/config` | `RECON_ENABLED` (bool, default `true`) |
| `internal/platform/metrics` | The three metrics + `reconciliation.Observer` methods |
| `cmd/wallet/app.go`, `main.go` | Start the scheduler in `serve` when enabled; `reconcile --job` command; usage text |
| `.golangci.yml` | depguard `reconciliation-deps`: imports `payment`, `ledger`, `transaction`, `bank`, `discrepancy`, `outbox` + platform only (architecture §3.2); add `reconciliation` to the other modules' deny lists where missing |

### Data (migrations)
```sql
job_runs(id uuid PK, job_name text NOT NULL,
  started_at timestamptz NOT NULL, finished_at timestamptz NOT NULL,
  status text NOT NULL CHECK (status IN ('SUCCESS','FAILED','SKIPPED')),
  items_checked int NOT NULL DEFAULT 0, items_fixed int NOT NULL DEFAULT 0,
  error text NULL)
INDEX (job_name, started_at DESC)
```
`SKIPPED` stays allowed by the CHECK (data-model §3) but this phase never writes it (decided 2026-11-10).

### API
None (HTTP). CLI:
```
wallet reconcile --job=<name>   run one reconciliation job once and exit
```
Exit `0` when the run ends `SUCCESS`, `1` when it ends `FAILED` or the job is unknown (stderr lists
the registered jobs), `2` for usage errors (missing `--job`). If another instance holds the job's
lock: print `job <name> is running elsewhere; skipped` and exit `0`. Needs only `DATABASE_URL`
(plus `BANK_BASE_URL` once a job needs the bank, T022).

### Behaviour and design notes
```text
type Result struct { Checked, Fixed int; Err error } // Err != nil → run FAILED
type Job struct {
    Name     string        // e.g. "pending_bank_ops"; [a-z_]+, unique
    Interval time.Duration // > 0
    Timeout  time.Duration // > 0, ≤ Interval
    Run      func(ctx context.Context) Result
}
func NewScheduler(db *database.DB, clk clock.Clock, log *slog.Logger, obs Observer) *Scheduler
func (s *Scheduler) Register(j Job)            // panics on invalid/duplicate or after Run (wiring bug)
func (s *Scheduler) Run(ctx context.Context) error   // one goroutine per job until ctx ends
func (s *Scheduler) RunOnce(ctx context.Context, name string) (ran bool, res Result, err error)
```
- **Per tick** (`RunOnce` does the same once):
  1. Take a connection from the pool and `SELECT pg_try_advisory_lock($key)`, where `$key` = the
     first 8 bytes of SHA-256(`"recon:" + name`) as int64 (stable across versions).
  2. Not acquired → log at debug, `recon_job_runs_total{status="skipped"}++`, no `job_runs` row
     (decided 2026-11-10), release the connection.
  3. Acquired → run the job with `context.WithTimeout(ctx, Timeout)`; a panic becomes a `FAILED`
     run with the panic text as `error`; then `pg_advisory_unlock` on **the same connection**,
     release it, and insert the `job_runs` row (`SUCCESS` when `Err == nil`, else `FAILED`,
     `error` = `Err.Error()` truncated to 2 000 characters). Metrics: runs total, duration,
     `recon_items_total{kind="checked"|"fixed"}`.
- **Timing:** each job's loop waits `Interval ± 10 %` (uniform jitter) between ticks, the first tick
  after one jittered interval (not at start-up, so a restart loop does not hammer the bank).
  Ticks never overlap for one job: the next wait starts after the run ends.
- **Shutdown:** `Run` returns `nil` after ctx ends and every running job finished (bounded by its
  Timeout). Recording the final `job_runs` row uses a context detached from the cancelled one
  (5 s cap), like the queue's marks.
- **Times** come from the clock (tests use a fake clock for `started_at`/`finished_at`; the tick
  waits use real timers).
- **Logs:** `job`, `status`, `items_checked`, `items_fixed`, `duration`, `error`.
- `serve`: when `RECON_ENABLED=true` → `g.Go(scheduler.Run)` and log "reconciliation started" with
  the job names; otherwise log "reconciliation disabled". With no job registered it still starts
  (logs an empty list) so T022/T023 only register jobs.

### Out of scope
The jobs themselves (T022, T023, P2-4), the daily wall-clock schedule (`daily_settlement`, P2-4),
an HTTP view of `job_runs`.

## Acceptance Criteria
- [x] AC-1 — Given a registered test job with interval 50 ms, when the scheduler runs for ~1 s, then the job ran several times, each run has a `job_runs` row (`SUCCESS`, its `items_checked`/`items_fixed`), and runs of one job never overlap.
- [x] AC-2 — Given two schedulers (two "instances") on the same database with the same job, when both run, then at no moment do two runs of that job execute at once (the job asserts it with a counter), and skipped ticks write no `job_runs` row but increase `recon_job_runs_total{status="skipped"}`.
- [x] AC-3 — A job returning `Err`, timing out, or panicking → `FAILED` row with the error text; the scheduler keeps running and the lock is released (the next tick runs).
- [x] AC-4 — `Register` panics on an empty/invalid name, duplicate name, `Interval ≤ 0`, `Timeout ≤ 0` or `> Interval`, or after `Run` started.
- [x] AC-5 — Cancelling ctx stops `Run` within the running job's timeout; the in-flight run's row is still recorded.
- [x] AC-6 — `wallet reconcile --job=<test job>` runs it once (row recorded, exit 0); unknown job → exit 1 listing jobs; no `--job` → exit 2; lock held elsewhere → "skipped", exit 0.
- [x] AC-7 — `RECON_ENABLED=false` → `serve` does not start the scheduler (log line); default → it starts.
- [x] AC-8 — The advisory key of a name is stable (fixed vector test) and differs between job names.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-3, AC-5 | integration | `TestScheduler_RunsAndRecords`, `TestScheduler_FailuresRecorded`, `TestScheduler_Shutdown` |
| AC-2 | integration | `TestScheduler_SingleRunnerAcrossInstances` |
| AC-4, AC-8 | unit | `TestRegister_Validation`, `TestLockKey_Stable` |
| AC-6, AC-7 | integration (cmd/wallet) | `TestReconcile_CLI`, `TestServe_ReconEnabledFlag` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Split of P2-2/P2-3 | two tasks / one / three | Answered | Three tasks: T021 scheduler, T022 pending_bank_ops, T023 dead_outbox + stuck_alert |
| 2 | Record lock-skipped ticks in `job_runs`? | no row, log + metric / write SKIPPED | Answered | No row; log + metric |
| 3 | Job thresholds configurable? | env vars with TDR defaults / only RECON_ENABLED | Answered | Env vars with TDR-009 values as defaults (used by T022/T023) |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Reconciliation split into T021 (scheduler), T022 (`pending_bank_ops`), T023 (`dead_outbox`, `stuck_alert`) | Yahya | roadmap |
| 2026-11-10 | Lock-skipped ticks: no `job_runs` row, only a log line and `recon_job_runs_total{status="skipped"}` | Yahya | this task |
| 2026-11-10 | Job thresholds are env vars defaulting to the TDR-009 values | Yahya | this task, T022 |
| 2026-11-10 | Advisory key = first 8 bytes of SHA-256("recon:"+name); first tick after one jittered interval; ±10 % jitter; no overlapping ticks per job | planning (technical detail of TDR-009 §1) | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | In Review | Implemented; verification passed (implementation.md) |
| 2026-11-10 | Approved | Review round 1: 1 Nit (review.md) |
| 2026-11-10 | Done | Finalized — PR opened |

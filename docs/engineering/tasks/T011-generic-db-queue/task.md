# T011 — Generic DB-backed queue (dispatcher + worker pool)

| Field        | Value |
|--------------|-------|
| ID           | T011 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T011-generic-db-queue` |
| Depends on   | T002 |
| PRDs         | PRD-001 NFR-3, NFR-4, NFR-10 |
| TDRs         | TDR-006 §2, §3, §5; TDR-007 §3; TDR-012 (`platform/queue`) |
| Docs changed | — |

## What
`internal/platform/queue`: the shared engine behind the outbox (T012) and the inbox (T015). It
knows nothing about tables. Each owner module implements a small `Store` over its own table with sqlc.

## Why
TDR-006 §2 (dispatcher, buffered channel 2×N, fixed workers, lease, `SKIP LOCKED`, wake-up),
§3 (outcomes, backoff with full jitter, base 1 s, cap 5 min, `DEAD` after max attempts),
§5 (graceful stop). TDR-007 §3 reuses it. Decided 2026-11-05: Store interface, not generic SQL.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/platform/queue/queue.go` | `Job`, `Result`, `Store`, `Handler`, `Config`, `Runner` |
| `internal/platform/queue/backoff.go` | `Backoff(attempt int, rnd) time.Duration` |
| `internal/platform/queue/queue_test.go` | Unit tests with an in-memory fake store and a fake clock |
| `internal/platform/queue/queuetest/` | Fake `Store` for other packages' unit tests |
| `internal/platform/metrics` | `queue_jobs_total{queue,result}` (`done`/`retry`/`dead`/`failed`), `queue_claimed_total{queue}`, `queue_handler_duration_seconds{queue}`; observer interface as in T002 |

```text
type Job struct { ID uuid.UUID; Type string; Payload []byte; Attempts int }
type Result struct{ kind; err error; reason string }
func Done() Result; func Retry(err error) Result; func Fail(reason string) Result
type Handler func(ctx context.Context, job Job) Result

type Store interface {
    // Claims up to n due jobs (PENDING with next_attempt_at <= now, or PROCESSING with an expired lease),
    // sets PROCESSING, locked_by, locked_until = now+lease, attempts+1, FOR UPDATE SKIP LOCKED.
    Claim(ctx context.Context, owner string, n int, lease time.Duration) ([]Job, error)
    MarkDone(ctx context.Context, id uuid.UUID) error
    MarkRetry(ctx context.Context, id uuid.UUID, next time.Time, lastErr string) error   // back to PENDING
    MarkDead(ctx context.Context, id uuid.UUID, lastErr string) error
    MarkFailed(ctx context.Context, id uuid.UUID, reason string) error                  // inbox uses FAILED; outbox maps to DONE (see T012)
}
type Config struct {
    Name string; Workers int; PollInterval, Lease, HandlerTimeout time.Duration
    MaxAttempts int; BatchSize int // default 2×Workers
}
func NewRunner(cfg Config, store Store, h Handler, clk clock.Clock, log *slog.Logger, obs Observer) *Runner
func (r *Runner) Run(ctx context.Context) error   // blocks until ctx is done and workers finished
func (r *Runner) Wake()                           // non-blocking signal; extra wakes are dropped
```

### Data (migrations)
None (tables belong to T012/T015).

### API
None.

### Behaviour and design notes
- **Dispatcher** (1 goroutine): on each tick of `PollInterval` **or** `Wake()`, if the channel has
  room, `Claim(owner, free slots, Lease)` and push the jobs to the buffered channel
  (capacity `BatchSize`). `owner` = hostname + PID + random suffix (one per Runner).
- **Workers** (`Workers` goroutines): each job runs with `context.WithTimeout(HandlerTimeout)`.
  Then:
  - `Done` → `MarkDone`.
  - `Retry(err)` → if `job.Attempts >= MaxAttempts` → `MarkDead(err)`, otherwise
    `MarkRetry(now + Backoff(job.Attempts), err)`.
  - `Fail(reason)` → `MarkFailed(reason)`.
  - A panic in the handler is recovered and treated as `Retry(panic error)` (logged with stack).
  - A `Mark*` error is logged; the lease expiry later reclaims the job (at-least-once).
- **Backoff**: full jitter `rand[0, min(5m, 1s·2^attempts))` (TDR-006 §3). The random source is
  injectable for tests.
- **Shutdown**: when ctx ends, the dispatcher stops claiming; jobs already in the channel are not
  started (their lease simply expires); running handlers finish within their own timeout; `Run`
  returns after all workers exit. The root shutdown timeout (25 s) bounds it.
- Config validation: Workers ≥ 1, MaxAttempts ≥ 1, durations > 0 — `NewRunner` returns an error.
- Logs: `queue`, `job_id`, `type`, `attempts`, `result`.
- `platform/queue` imports only platform packages (depguard rule already covers `internal/platform/**`).

### Out of scope
The tables and real stores (T012/T015), `LISTEN/NOTIFY`, ordering guarantees, priorities.

## Acceptance Criteria
- [ ] AC-1 — Given 10 due jobs and 3 workers, then all 10 handled once and `MarkDone` called 10 times; never more than 3 handlers run at once.
- [ ] AC-2 — Given a handler returning `Retry`, then `MarkRetry` with `next = now + backoff`, `0 ≤ backoff < min(5m, 1s·2^attempts)`. Once attempts reach `MaxAttempts` → `MarkDead`.
- [ ] AC-3 — `Fail(reason)` → `MarkFailed(reason)`. A panic → treated as retry, the runner keeps working.
- [ ] AC-4 — A handler longer than `HandlerTimeout` sees its ctx canceled.
- [ ] AC-5 — `Wake()` makes the dispatcher claim before the next poll tick; many `Wake()` calls never block.
- [ ] AC-6 — Ctx canceled while 2 handlers are running → they finish, no new claims happen, `Run` returns nil.
- [ ] AC-7 — `Backoff` table: attempt 1 < 2s, attempt 10 < 5 min cap, never negative.
- [ ] AC-8 — Invalid config → `NewRunner` error. Metrics counters increase per result.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | unit | `TestRunner_ProcessesAllOnce_RespectsWorkers` |
| AC-2, AC-3 | unit | `TestRunner_RetryThenDead`, `TestRunner_FailAndPanic` |
| AC-4 | unit | `TestRunner_HandlerTimeout` |
| AC-5 | unit | `TestRunner_WakeClaimsEarly` |
| AC-6 | unit | `TestRunner_GracefulShutdown` |
| AC-7 | unit | `TestBackoff` |
| AC-8 | unit | `TestNewRunner_InvalidConfig`, `TestRunner_Metrics` |

The real `SKIP LOCKED` / lease behaviour is tested against Postgres by the owners' stores (T012, T015).

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | How the queue is parameterized per table | Store interface per owner / generic SQL by table name | Answered | Store interface |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | `platform/queue` = engine + `Store` interface; owners implement it with sqlc | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

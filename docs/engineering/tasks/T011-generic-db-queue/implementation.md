# T011 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T011-generic-db-queue`, stacked on T010 / T009; rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **`internal/platform/queue`**:
  - Types: `Job`, `Result` (`Done`, `Retry(err)`, `Fail(reason)`), `Handler`, `Store` (`Mark*` take the claimed job, see Review fix), `Observer`, `Config` (validated), and `Runner` with `NewRunner`, `Run` and `Wake`.
  - **Dispatcher:** claims on start, then on every `PollInterval` tick or `Wake()`. It claims only as many jobs as the buffered channel (`BatchSize`, default 2 × Workers) has room for. Claim errors are logged and it keeps going.
  - **Workers:** a fixed pool. Each job runs under `HandlerTimeout`, on a context detached from the root, so a running handler finishes during shutdown. Then:
    - `Done` → `MarkDone`; `Fail` → `MarkFailed`;
    - `Retry` → `MarkDead` once attempts ≥ `MaxAttempts`, otherwise `MarkRetry(now + Backoff)`;
    - a panic is recovered (stack logged) and treated as `Retry`;
    - `Mark*` calls run with their own 5 s timeout; a failure is logged and the lease expiry reclaims the job.
  - **Shutdown:** the dispatcher stops claiming; jobs still in the channel are not started (their lease expires); `Run` returns nil after the workers exit.
  - **`Backoff(attempt, rnd)`:** full jitter in `[0, min(5 min, 1 s·2^attempt))`, overflow-safe, with an injectable random source.
  - **`owner`** = hostname-pid-random.
- **`internal/platform/queue/queuetest`**: a thread-safe in-memory `Store` driven by a clock, for unit tests of the outbox and inbox handlers.
- **`internal/platform/metrics`**: `queue_jobs_total{queue,result}`, `queue_claimed_total{queue}`, `queue_handler_duration_seconds{queue}`; `Metrics` implements `queue.Observer`.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestRunner_ProcessesAllOnce_RespectsWorkers` (10 jobs, 3 workers, each once, max 3 concurrent) | ✅ |
| AC-2 | `TestRunner_RetryThenDead` (next = now + backoff with the largest jitter: < 2 s, < 4 s; DEAD at attempt 3) | ✅ |
| AC-3 | `TestRunner_FailAndPanic` (Fail → FAILED with the reason; panic → retry; the next job still runs) | ✅ |
| AC-4 | `TestRunner_HandlerTimeout` (ctx `DeadlineExceeded`) | ✅ |
| AC-5 | `TestRunner_WakeClaimsEarly` (poll 1 h; Wake → handled at once; 1000 Wakes never block) | ✅ |
| AC-6 | `TestRunner_GracefulShutdown` (2 running handlers finish with a live ctx, no claim after cancel, a late job stays PENDING, `Run` returns nil) | ✅ |
| AC-7 | `TestBackoff` (1 → < 2 s, 10 → capped, 1000 → no overflow, 1500 random samples in range) | ✅ |
| AC-8 | `TestNewRunner_InvalidConfig` (7 cases); per-result counters in the runner tests + `TestMetrics_Queue` | ✅ |
| extra | `TestRunner_ClaimErrorKeepsRunning` | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 384 passed |
| Flakiness | `go test -race -count=20 ./internal/platform/queue/` ✅ |
| Coverage `internal/platform/queue` | 95.0% |
| compose / HTTP | not applicable: nothing is wired yet (outbox T012, inbox T015) |

## Review fix (round 2, found while planning T012)
- `queue.Store.Mark*` take the claimed `Job` (not only its ID) so a store can ignore stale marks: update only if the row is still PROCESSING with `job.Attempts`. `queuetest.Store` follows the rule; `TestRunner_StaleMarkIgnored` proves it.

## Deviations from the plan
- `Store.Claim` is called with the root ctx, so it stops when shutdown starts; `Mark*` calls use a detached ctx with a 5 s timeout, so results of finished handlers are still saved during shutdown.
- `queue.Observer` is defined in `queue` and implemented by `metrics.Metrics` (the same pattern as `database.TxObserver`).

## Follow-ups (not done)
- None.

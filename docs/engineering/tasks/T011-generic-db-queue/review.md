# Review — T011

## Round 2 (2026-11-09)

Verdict: **Approved**

Found while planning T012 (outbox AC-3, "crashed instance, lease expires, another runner
re-claims"): with `Mark*(ctx, id)`, a runner whose lease expired could still mark the job when
its slow handler finally returned. For example, its `MarkRetry` would push a job that another
runner already finished back to PENDING and deliver it again.

| # | Severity | File:line | Rule | Finding | Status |
|---|----------|-----------|------|---------|--------|
| 2 | **Major** | `internal/platform/queue/queue.go` (`Store`) | B: at-least-once without corrupting state | Stale marks from an owner that lost its lease were not prevented by the interface. | **Fixed**: `Mark*` now receive the claimed `Job`. Stores update only `WHERE status = 'PROCESSING' AND attempts = job.Attempts` (each claim increments attempts). `queuetest.Store` enforces the same rule and counts ignored marks. `TestRunner_StaleMarkIgnored` (two runners, A stalls past its lease, B finishes, A's late Retry is ignored; stable over 20 `-race` runs). Coverage 96%. |

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The runner follows TDR-006 §2/§3/§5: bounded claiming, a fixed worker pool, at-least-once
delivery with a lease, full-jitter backoff, DEAD after max attempts, and graceful stop. Tests
cover every AC, are stable over 20 `-race` runs, and give 95% coverage. No findings above Nit; I
reviewed this alone.

| # | Severity | File:line | Rule | Finding | Status |
|---|----------|-----------|------|---------|--------|
| 1 | Nit | `internal/platform/queue/queue.go` (`runOne`) | Observability | The handler duration uses the injected clock, so it is 0 in tests that use a fake clock. | Fine: production uses the system clock. |

### Checked and fine
- **A:** platform only (clock); no tables; the `Store`/`Observer` interfaces are consumer-defined; `queuetest` is a reusable fake.
- **B:** goroutine ownership is clear:
  - the dispatcher closes the channel when it exits, so the workers exit and `Run` waits for them;
  - nothing leaks, and only the dispatcher sends, after counting free slots, so a send never blocks;
  - `Wake` is a non-blocking send on a 1-slot channel.
- **C:** a panic becomes a retry with the stack logged; `Mark*` failures are logged and recovered by the lease; overflow-safe backoff; validated config.
- **D:** no data or API in this task.
- **E:** AC-1…8 plus claim-error resilience; deterministic backoff assertions with an injected jitter.

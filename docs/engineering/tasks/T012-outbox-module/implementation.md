# Implementation — T012 Outbox module

## Summary
The transactional outbox (TDR-006). `outbox.Service.Enqueue(ctx, tx, Event)` inserts a PENDING
event in the caller's unit of work, so it is delivered only if that transaction commits.
`Run` delivers events with the generic queue engine (T011): a dispatcher plus
`OUTBOX_WORKERS` workers, `SKIP LOCKED` claims, a 60 s lease, a 10 s handler timeout, and
backoff then DEAD after `OUTBOX_MAX_ATTEMPTS`. `serve` starts it when `OUTBOX_ENABLED`
(default true). The backlog gauges are refreshed every 15 s.

## Files
| Path | Change |
|------|--------|
| `migrations/000007_outbox.up/down.sql` | `outbox_events`, partial index `(status, next_attempt_at) WHERE status IN ('PENDING','PROCESSING')`, index `(aggregate_id)` |
| `internal/outbox/model.go` | `Event`, `Handler` (= `queue.Handler`), `ErrUnknownEventType`, `Querier`, `Observer` |
| `internal/outbox/service.go` | `NewService`, `Register`, `Enqueue`, `Wake`, `Run`, `RefreshBacklog`; dispatch to the registered handler |
| `internal/outbox/store.go`, `queries.sql` | `queue.Store`: claim (TDR-006 §2 SQL), and `Mark*` guarded by `status = 'PROCESSING' AND attempts = job.Attempts`; `MarkFailed` → DONE + `last_error` |
| `internal/outbox/internal/sqlcgen/` | generated; every module's `models.go` also gains `OutboxEvent` (sqlc models list all tables) |
| `sqlc.yaml` | outbox block |
| `internal/platform/config` | `OUTBOX_ENABLED=true`, `OUTBOX_WORKERS=8` (≥ 1), `OUTBOX_MAX_ATTEMPTS=10` (≥ 1), `QUEUE_POLL_INTERVAL=500ms` (> 0) |
| `internal/platform/metrics` | `outbox_pending_total`, `outbox_oldest_pending_age_seconds`, `outbox_dead_total` |
| `cmd/wallet/app.go` | `newAPI` builds the outbox (with the metrics as observer); `serve` runs it in the errgroup when enabled and logs "outbox delivery started" or "outbox delivery disabled" |
| `.golangci.yml` | depguard `outbox-deps`: outbox imports no business module (probed: importing `wallet` fails lint) |

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestOutbox_CommitDelivers_RollbackDoesNot` (rolled-back event has no row; committed one delivered once with the same JSON payload, DONE + `processed_at`) | ✅ |
| AC-2 | `TestOutbox_TwoRunnersExactlyOnce` (2 instances × 4 workers, 100 events, each handled once, all `attempts = 1`) | ✅ |
| AC-3 | `TestOutbox_ExpiredLeaseReclaimed` (instance A hangs holding the claim; a valid lease is not taken over; after the clock passes the lease, B claims with attempts 2; A's late mark is stale and changes nothing) | ✅ |
| AC-4 | `TestOutbox_RetryThenDead` (PENDING, `next_attempt_at` in (now, now+2 s], `last_error`; not due → not claimed; DEAD at attempt 3, `outbox_dead_total` +1) | ✅ |
| AC-5 | `TestOutbox_FailEndsDone` | ✅ |
| AC-6 | `TestOutbox_WakeAfterCommit` (10 s poll: without Wake not delivered within 1 s; with Wake delivered in < 2 s) | ✅ |
| AC-7 | `TestEnqueue_UnknownType` (unit) | ✅ |
| AC-8 | `TestConfig_Outbox`, `TestConfig_LoadWallet_Defaults`, `TestServe_OutboxDisabled` (disabled: a due event stays unclaimed for 1 s; enabled: it is claimed) | ✅ |
| AC-9 | `TestOutbox_Gauges` (empty → 0/0; PENDING + PROCESSING counted, DONE not; oldest age), `TestMetrics_Outbox` | ✅ |

Also: `TestOutbox_UnknownTypeIsRetried`, `TestOutbox_RunTwiceAndRegisterAfterRun`, `TestRegister_WiringBugsPanic`,
`TestEnqueue_PayloadNotEncodable`, `TestNewService_InvalidConfig`, `TestNewService_DefaultLeaseAndTimeout`,
`TestDeliver_CallsRegisteredHandler`. The outbox integration tests passed 5 runs in a row with `-race`.

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, gofmt, vet, golangci-lint, sqlc-check, build, unit + integration with `-race`) | ✅ exit 0, lint 0 issues |
| Tests | ✅ 411 passed |
| Coverage `internal/outbox` (unit + integration) | 95.5% |
| `make up` + `make e2e` | ✅ |
| Live: `/metrics` | `outbox_pending_total 0`, `outbox_oldest_pending_age_seconds 0`, `outbox_dead_total 0`; log `outbox delivery started workers=8` |
| Live: a due event of an unregistered type inserted with psql | claimed and retried with backoff (`attempts` 4 after ~17 s, `last_error = outbox: unknown event type: "live.check"`); `outbox_pending_total 1`, age ≈ 14.6 s, `queue_claimed_total{queue="outbox"} 4`, `queue_jobs_total{…result="retry"} 4` |

No HTTP endpoints were added (API: none).

## Deviations from the plan
- The task places "lease 60 s, handler timeout 10 s" among the config constants; they are
  `outbox.DefaultLease` / `outbox.DefaultHandlerTimeout`, used when `outbox.Config` leaves them
  zero (only tests could set them; no env var was added).
- The backlog gauges are refreshed by a goroutine next to the runner, not inside the queue
  engine's dispatcher (which knows nothing about tables). `RefreshBacklog` is exported for it and
  the tests.
- An event whose type has no handler at delivery time (e.g. enqueued by a newer version during a
  rolling deploy) is retried, then DEAD, instead of being dropped. The task did not say.
- An empty backlog reports age exactly 0 (found in the live check: PostgreSQL's microsecond
  rounding gave 1.2e-08 s).
- Lease and due times use the app clock (`clock.Clock`), like every other module, instead of
  the database's `now()` in the TDR-006 SQL; see review finding #1.
- AC-3's "crash" is a handler that hangs while holding the claim, and the lease expires by
  moving the fake clock past the default 60 s lease instead of a short lease.

## Follow-ups
None.

# Implementation — T021 Reconciliation scheduler

## Summary
The new module `internal/reconciliation` holds the scheduler that the reconciliation jobs will run on.
- **Jobs:** each job is `{Name, Interval, Timeout, Run}`. `Register` validates it and panics on a wiring bug.
- **Scheduling:** `Run` starts one goroutine per job. Each goroutine waits `Interval ± 10 %`, then ticks. The first tick comes after one interval, and runs of the same job never overlap.
- **One runner at a time:** a tick takes `pg_try_advisory_lock(LockKey(name))` on one pooled connection. If another instance holds the lock, the tick is skipped: a debug log and the metric `status="skipped"`, but no `job_runs` row.
- **Running:** otherwise the job runs with its timeout; a panic is recovered as a FAILED result. The lock is then released on the same connection. If unlocking fails, the connection is closed rather than returned to the pool, which drops the lock.
- **Recording:** every real run gets a `job_runs` row (status, items checked and fixed, the error cut to 2 000 characters) and the metrics are updated. Unlocking and recording use a detached 5 s context, so they also happen during shutdown.
- **`RunOnce`:** runs the same code once now, for the CLI.

`serve` starts the scheduler when `RECON_ENABLED` is true (the default) and logs the job list; otherwise it logs "reconciliation disabled".

`wallet reconcile --job=NAME` runs one job once:
- exit 0 on success, or when another instance is running it ("skipped");
- exit 1 when the run fails or the job is unknown (the registered jobs are listed);
- exit 2 for usage errors.

It needs only `DATABASE_URL`, and builds the bank client when `BANK_BASE_URL` is set (for T022).

No business job is registered yet: `reconJobs` returns none until T022 and T023.

## Files
| Path | Change |
|------|--------|
| `migrations/000010_job_runs.up/down.sql` | `job_runs` + index `(job_name, started_at DESC)` |
| `internal/reconciliation/scheduler.go`, `queries.sql`, sqlcgen; `sqlc.yaml` | Module |
| `internal/platform/config` | `RECON_ENABLED` (default true) |
| `internal/platform/metrics` | `recon_job_runs_total{job,status}`, `recon_job_duration_seconds{job}`, `recon_items_total{job,kind}` |
| `cmd/wallet/main.go`, `app.go` | `reconcile --job` command and usage; `api.recon`; `reconJobs` registration hook; scheduler in `serve` |
| `configs/.env.example` | `RECON_ENABLED` |
| Tests | `internal/reconciliation/scheduler_internal_test.go`, `scheduler_integration_test.go`; `cmd/wallet` (`TestReconcile_CLI`, `TestServe_ReconEnabledFlag`, usage cases); `config_test.go` |

The task's file list had a separate `lock.go` and `store.go`. The lock and record code is short, so it lives in `scheduler.go` with the generated queries.

`.golangci.yml` needed no change: every module's deny list already contains `reconciliation`, and `reconciliation` imports only platform for now. A `reconciliation-deps` rule will be added when T022 brings its first business imports.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestScheduler_RunsAndRecords` (≥4 runs at a 50 ms interval, one SUCCESS row each with checked/fixed, no overlap) | ✅ |
| AC-2 | `TestScheduler_SingleRunnerAcrossInstances` (two schedulers, no concurrent run, ≥3 skipped ticks counted, rows = successful runs only) | ✅ |
| AC-3 | `TestScheduler_FailuresRecorded` (error → FAILED with text, timeout → "deadline exceeded", panic → "panic: boom", then SUCCESS: the lock was released), `TestScheduler_LongErrorTruncated` | ✅ |
| AC-4 | `TestRegister_Validation` (7 invalid jobs, duplicate, after Run) | ✅ |
| AC-5 | `TestScheduler_Shutdown` (cancel during a run → Run returns, the run's row recorded; Run twice → error) | ✅ |
| AC-6 | `TestReconcile_CLI` (success exit 0 + output; FAILED exit 1; unknown job exit 1 listing jobs; two rows; lock held elsewhere → "skipped", exit 0, no call), `TestCLI_UsageErrors_Exit2` (missing/empty `--job`, extra argument, `--job` on another command), `TestScheduler_RunOnceSkipsWhileLocked` | ✅ |
| AC-7 | `TestServe_ReconEnabledFlag` (false → "reconciliation disabled", no run; default → "reconciliation started" with the job, runs), `TestConfig_ReconEnabled` | ✅ |
| AC-8 | `TestLockKey_Stable` (vectors computed with Python `hashlib`), `TestJittered_WithinTenPercent` | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, gofmt, vet, golangci-lint, sqlc-check, build, unit + integration `-race`) | ✅ 0 issues |
| Full suite `-race -tags integration` | ✅ 673 passed, 0 failed |
| Coverage `internal/reconciliation` | 94.0% |
| Compose | `serve` logs `"reconciliation started","jobs":[]`. In the container, `/app reconcile --job=pending_bank_ops` → `unknown job … (registered: [])`, exit 1; `/app reconcile` → usage, exit 2. `job_runs` is empty, as expected with no jobs. |
| `make e2e` | ✅ (28.9 s) |
| Ledger integrity | not affected (no money code changed); the e2e checks passed |

`TestServe_ReconEnabledFlag` first used a fixed 2 s wait. Under `-race`, serve's startup (the bcrypt warm-up) can take longer than that, so the test now waits for the log line or the first run, up to 30 s.

## Deviations
- The lock and record code is in `scheduler.go` instead of separate `lock.go` / `store.go` files.
- The `reconciliation-deps` depguard rule is deferred to T022 (see Files).

## Follow-ups
- T022 registers `pending_bank_ops` through `reconJobs`, and T023 registers `dead_outbox` and `stuck_alert`.

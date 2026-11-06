# Review — T002 (2026-11-05, round 1)
Verdict: **Approved** (3 Minor and 4 Nit, all optional)

Summary: The four primitives match the task's How and every AC has a passing test. Package
placement and imports are clean (`platform` only, no business imports), money code is
integer-only and overflow-safe, and `InTx` implements TDR-010 §5 exactly. The findings are
edge cases and test precision; none affects money correctness today, because no caller exists yet.

Note on process: the user asked to start the review before verification steps 9–10 had finished.
They completed before this review was written: compose up, HTTP checks pass (see `implementation.md`).

## Findings
| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | `internal/platform/database/tx.go:132`, `database.go:30`, `config/config.go:63` | B (lock_timeout, TDR-010 §5) | `milliseconds()` truncates to whole ms, but validation only requires `> 0`. `DB_LOCK_TIMEOUT=500us` passes validation and becomes `set_config('lock_timeout','0ms')`, and in PostgreSQL `0` means **no timeout**. The guard is then silently disabled. The same applies to the statement timeout. | Require `>= 1ms` in `TxSettings.validate` and `config.Wallet.validate` (the error names the variable), or round up to 1 ms. Add a table row to both tests. |
| 2 | Minor | `internal/platform/database/tx.go:60` | C (metrics accuracy) | `ObserveTxRetry` is called **before** `sleep`. If ctx is canceled during the wait, InTx returns `context.Canceled` but `db_tx_retries_total` already counted a retry that never ran. | Call `ObserveTxRetry(reason)` after `sleep` returns nil. |
| 3 | Minor | `internal/platform/database/tx_integration_test.go:270` | E (AC-17 wording) | `TestInTx_ContextCanceledDuringBackoff` cancels ctx inside `fn`, i.e. **before** the wait starts. It proves "no further attempt after cancel", but not "canceled *during* the wait". `TestSleep_ReturnsOnCancel` also uses an already-canceled ctx. | Add an internal (package `database`) test that sets `db.backoff` to a long duration and cancels with `time.AfterFunc(20ms, cancel)` while InTx sleeps. Or rename the existing test to what it proves and add that one. |
| 4 | Nit | `internal/platform/database/tx.go:68` | C (simplicity) | `runOnce` declares a named result `(err error)` that is never used as such. | Drop the name: `… ) error {`. |
| 5 | Nit | `internal/platform/metrics/metrics.go:53` | C (duplication) | The three reason strings are repeated in `metrics` and `database` (`reasonDeadlock` …). If one side changes, the pre-created series drift. | Export the reasons from `database` (e.g. `database.TxRetryReasons()`) and let `cmd/wallet` pass them to metrics, or accept the duplication with a comment pointing to `database/tx.go`. |
| 6 | Nit | `internal/platform/database/database.go:24` | C (single source of truth) | `DefaultTxSettings()` repeats the values of the `envDefault` tags in `config.Wallet`. Already noted as a deviation in `implementation.md`. | Keep it (tests need it), but add a comment that config is the source for the service. |
| 7 | Nit | `internal/platform/clock/clock.go:23` | C (no global mutable state) | `Cairo` is an exported package variable, so any package could reassign it. The task allowed it ("read-only after init"), but nothing enforces that. | Optional: make it unexported with `func Cairo() *time.Location`, or keep it and rely on review. |

## Checked and fine
- **A. Architecture:** new packages are in `internal/platform/{money,id,clock}` and `clocktest/`, as TDR-012 says. `go list` shows imports from stdlib, pgx, uuid, prometheus and env only, with no business modules (depguard agrees). No speculative interfaces: `TxObserver` has 2 implementations (metrics and a test fake), and `Clock` has `System` and `Fake`.
- **B. Money:** no float types or `ParseFloat`, and `forbidigo` passes. The `Add`/`Sub` overflow conditions are checked on both sides, including `MinInt64`. Currency equality is checked, and the zero value is rejected. `Format` uses integers only and handles `MinInt64` after the G115 fix.
- **B. Unit of work:** `set_config(…, true)` = `SET LOCAL` and is proven not to leak. Retries cover exactly 40001/40P01/55P03. `57014` is not retried. The `ErrTryAgain` error wraps both the sentinel and the `PgError` (`%w: %w`). Rollback uses `context.WithoutCancel`. A panic rolls back and re-panics. The doc comment states the no-network and no-nesting rules (TDR-010 §6). The warn log plus returned error is required by AC-14, so it is not a "log and return" smell.
- **B. Clock:** the `CairoDay` binary search is exact to the second. DST 2026 values were verified against the embedded tzdata. `d+1` normalization is done in UTC, so month and year ends work.
- **C. Go style:** gofmt, vet and golangci-lint are clean (0 issues). Names: `money.New`, `NewPositive`, `ErrXxx`, `DB.InTx`, receivers `m`, `db`, `f`; no stutter. Exported identifiers have doc comments. No `init()`: `Cairo` loads through a var initializer, and the panic on failure is documented. Errors are wrapped with the package prefix. The context is first everywhere, and `sleep` watches it.
- **C. Config:** one `validate()` reports every invalid variable by name. Defaults are as decided. `.env.example` is updated.
- **D. Data/API:** no migrations and no endpoints, as planned. `/readyz` and `/metrics` behave the same (HTTP checks).
- **E. Tests:** all 19 ACs are mapped and pass. Unit tests are table-driven where natural. Concurrency tests (`TestFake_Concurrent`, the deadlock test) pass `-race`. The InTx integration suite passed 5 runs in a row. T001 tests were adapted to the new `Open` signature, not weakened. Coverage: money/id/metrics 100 %, clock 93 %, database 83 %.
- **Plan conformance:** every row of the "Modules and files" table is done. README unchanged is justified (it defers to `.env.example`). Nothing out of scope was added.

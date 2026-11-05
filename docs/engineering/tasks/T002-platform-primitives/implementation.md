# T002 — Implementation

| Field   | Value |
|---------|-------|
| Branch  | `task/T002-platform-primitives` |
| Date    | 2026-11-05 |
| Status  | In Review |

## Summary
Added the four platform building blocks the money tasks need. There is no business logic and
no migration.

- **`internal/platform/money`** — `Money` (unexported `amount int64` + `currency`), `New` (any
  sign), `NewPositive` (> 0), overflow-checked `Add`/`Sub`, `Format`/`String`, `ParseCurrency`
  (only `EGP`), and four sentinel errors. Integer arithmetic only: `Format` divides before
  negating, so `math.MinInt64` works without an unsigned conversion.
- **`internal/platform/id`** — `New()` = UUIDv7.
- **`internal/platform/clock`** — `Clock`, `System` (UTC), `Cairo` location (the package embeds
  `time/tzdata`), and `CairoDay(t)`. `CairoDay` searches, to the second, around the nominal
  midnight for the first instant of the Cairo date. It does not trust `time.Date` for a midnight
  that does not exist, so it is exact on the 23 h and 25 h DST days.
  `clock/clocktest.Fake` (mutex, always UTC).
- **`internal/platform/database`** — `Open(ctx, url, Options)` now returns `*DB`
  (`Pool`, `Ping`, `Close`, `InTx`). `TxSettings` are validated, and `DefaultTxSettings()` is
  provided for tests and tools. `InTx` works like this:
  - Each attempt runs `BEGIN`, then `set_config('lock_timeout'|'statement_timeout', …, true)`,
    then `fn`, then `COMMIT`.
  - It retries on `40001`/`40P01`/`55P03`, waiting a random 10–50 ms between attempts, up to
    `MaxRetries`.
  - When the retries run out it returns an error matching both `ErrTryAgain` and the last
    `*pgconn.PgError`, reports `ObserveTxTryAgain`, and logs one warning.
  - Other errors are returned unchanged.
  - A canceled ctx stops the wait. A panic rolls back and is re-raised.
  - Rollback uses `context.WithoutCancel`.
- **`internal/platform/metrics`** — `db_tx_retries_total{reason}`, with the three reasons
  pre-created at 0, and `db_tx_try_again_total`. `*Metrics` implements `database.TxObserver`.
- **`internal/platform/config`** — `DB_LOCK_TIMEOUT` (2s), `DB_STATEMENT_TIMEOUT` (5s),
  `DB_TX_MAX_RETRIES` (3). Validation now runs in one `validate()` that reports every invalid
  variable by name.
- **`cmd/wallet`** — `connect` opens `*database.DB` with the config settings, the logger and the
  metrics as observer (migrate commands pass no observer). Readiness uses `db.Ping`. Behaviour is
  unchanged.
- `configs/.env.example` documents the three variables. The README defers to that file, so the
  README needed no change.

## Files
| Path | Change |
|------|--------|
| `internal/platform/money/money.go`, `money_test.go` | new |
| `internal/platform/id/id.go`, `id_test.go` | new |
| `internal/platform/clock/clock.go`, `clock_test.go`, `clocktest/clocktest.go` | new |
| `internal/platform/database/database.go` | `DB`, `Options`, `TxSettings`, `TxObserver`, `Open` signature |
| `internal/platform/database/tx.go`, `tx_test.go`, `tx_integration_test.go` | new |
| `internal/platform/database/migrate_integration_test.go` | uses the new `Open` |
| `internal/platform/metrics/metrics.go`, `metrics_test.go` | tx counters |
| `internal/platform/config/config.go`, `config_test.go` | 3 variables + validation |
| `cmd/wallet/app.go`, `app_integration_test.go`, `main_test.go` | wiring + tests |
| `configs/.env.example` | 3 variables |

Migrations: none.

## Acceptance Criteria → tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestNew_Sign`, `TestNewPositive` | ✅ |
| AC-2 | `TestParseCurrency` | ✅ |
| AC-3 | `TestAdd_Overflow`, `TestSub_Overflow`, `TestAddSub_CurrencyMismatch`, `TestAdd`, `TestSub` | ✅ |
| AC-4 | `TestFormat` (incl. `MaxInt64`, `MinInt64`) | ✅ |
| AC-5 | `make lint` — 0 issues (`forbidigo` covers `internal/platform/money/`) | ✅ |
| AC-6 | `TestNew_Version7`, `TestNew_UniqueAndOrdered` | ✅ |
| AC-7 | `TestSystem_UTC`, `TestFake_SetAdvance`, `TestFake_Concurrent` (`-race`) | ✅ |
| AC-8 | `TestCairoDay_Winter`, `TestCairoDay_Boundary` | ✅ |
| AC-9 | `TestCairoDay_DSTStart2026` (23 h), `TestCairoDay_DSTEnd2026` (25 h); expected values match Go's embedded tzdata | ✅ |
| AC-10 | `TestConfig_LoadWallet_Defaults/Overrides/InvalidValuesNameTheVariable`, `TestServe_InvalidConfig_Exit1NamesVariable` | ✅ |
| AC-11 | `TestInTx_Commit`, `TestInTx_RollbackOnError` | ✅ |
| AC-12 | `TestInTx_SetsLocalTimeouts` (`1500ms`/`4s` inside, `0`/`0` on pooled connections after) | ✅ |
| AC-13 | `TestInTx_DeadlockRetried` (real deadlock forced with a barrier; `deadlock` retries ≥ 1; both committed once) | ✅ |
| AC-14 | `TestInTx_LockTimeoutExhausted_ErrTryAgain` (3 attempts, `ErrTryAgain` + `55P03`, 2 retries, 1 try-again, 1 warn log) | ✅ |
| AC-15 | `TestInTx_RetryClassification` (unit), `TestInTx_SerializationFailureRetried` | ✅ |
| AC-16 | `TestInTx_StatementTimeoutNotRetried` (`57014`, 1 call) | ✅ |
| AC-17 | `TestInTx_ContextCanceledDuringBackoff`, `TestSleep_ReturnsOnCancel` | ✅ |
| AC-18 | `TestInTx_PanicRollsBack` | ✅ |
| AC-19 | all T001 suites pass; `TestMetrics_TxRetries`; `cmd/wallet` `TestMetrics_Exposed` checks `db_tx_try_again_total 0` and `db_tx_retries_total{reason="deadlock"} 0`; HTTP check below | ✅ |

Extra: `TestTxSettings_Validate`, `TestRetryBackoff_Bounds`, `TestMilliseconds`.
The InTx integration tests were also run 5 times in a row (`-count=5`): all passed (timing-sensitive deadlock and lock-timeout tests are stable).

## Verification
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `make tidy-check` | ✅ no diff |
| 2 | Format | `make fmt-check` | ✅ |
| 3 | Vet | `make vet` (tags integration, e2e) | ✅ |
| 4 | Lint | `make lint` (golangci-lint v2.15.0) | ✅ 0 issues — the first run found 1 gosec G115 (`int64→uint64` in `Format`); fixed by rewriting `Format` without the conversion, then **all** steps re-run |
| 5 | Build | `make build` | ✅ |
| 6 | Unit tests | `go test -race -count=1 ./...` | ✅ 95 tests/sub-tests pass |
| 7 | Integration | `go test -race -count=1 -tags integration ./...` | ✅ 116 tests/sub-tests pass, 0 fail |
| 8 | Coverage (unit + integration) | `go test -tags integration -cover …` | money 100 %, id 100 %, metrics 100 %, clock 93.3 %, database 83.4 %, config 87.9 %, cmd/wallet 86.2 % (`clocktest` shows 0 % because it is exercised from `clock_test`, another package) |
| 9 | Run the service | `POSTGRES_HOST_PORT=5433 make up` | ✅ postgres, migrate, wallet, banksim healthy. Two environment problems first: Docker Hub was very slow (BuildKit timed out fetching `docker/dockerfile:1`; fixed by pre-pulling it), and host port 5432 is taken by another project's container (`ytp-local-postgres`), so 5433 was used through the existing override |
| 10 | HTTP checks | see below | ✅ |
| 11 | Ledger integrity | not applicable — no ledger yet (T003) | — |
| 12 | Tear down | `docker compose down -v` | ✅ |

## HTTP checks
T002 adds no endpoint. These checks confirm that the new wiring changed nothing (AC-19) and that the new config is validated (AC-10) in the real container.

| Request | Expected | Actual |
|---------|----------|--------|
| `GET :8080/healthz` | 200 `{"status":"ok"}` + `X-Request-ID` | ✅ 200, `X-Request-Id: 01a1ad2d-…` |
| `GET :8080/readyz` | 200, database ok, migrations ok | ✅ `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}` |
| `GET :8080/metrics` | lists the tx counters | ✅ `db_tx_retries_total{reason="deadlock\|lock_timeout\|serialization_failure"} 0`, `db_tx_try_again_total 0` |
| `GET :9090/healthz` (banksim) | 200 | ✅ |
| `wallet migrate status` | `version=1 dirty=false latest=1` | ✅ |
| `wallet serve` with `DB_LOCK_TIMEOUT=0s` | exit 1, names the variable | ✅ `config: DB_LOCK_TIMEOUT must be positive, got 0s`, exit 1 |
| … with `DB_STATEMENT_TIMEOUT=later` | exit 1, names the variable | ✅ `invalid value for DB_STATEMENT_TIMEOUT: … invalid duration "later"` |
| … with `DB_TX_MAX_RETRIES=-1` | exit 1, names the variable | ✅ `DB_TX_MAX_RETRIES must not be negative, got -1` |

## Deviations from the plan
- None in behaviour. Small implementation choices, all inside the task's How:
  - `database.DefaultTxSettings()` was added so tests/tools don't repeat the TDR-010 numbers. The
    service still takes its values from config.
  - `NewMigrator` keeps taking the pool (via `db.Pool()`), which the task allowed.
  - The retry metric series are pre-created at 0 so `/metrics` lists them before the first retry (AC-19).

## Follow-ups (not done)
- None required by this task. `ErrTryAgain` → `503 TRY_AGAIN` mapping arrives with the first
  money endpoint (T008), as planned.

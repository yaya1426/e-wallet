# T002 — Platform primitives for money code: money, id, clock, unit of work

| Field        | Value |
|--------------|-------|
| ID           | T002 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T002-platform-primitives` |
| Depends on   | T001 |
| PRDs         | PRD-001 BR-1..BR-4 (money), NFR-1, NFR-2, NFR-7; §5.6 (Cairo day) |
| TDRs         | TDR-002, TDR-003, TDR-010, TDR-012, TDR-013 |
| Docs changed | Roadmap planning (2026-11-05): PRD-001…004 and TDR-003…011 → `Accepted`; TDR-003 §3 (`New`/`NewPositive`, unexported fields); TDR-010 §5 (config names, jitter, 55P03); new TDR-016 + TDR-007/009/012, architecture, data model (`discrepancy` module); PRD-002 §7; new `engineering/roadmap.md`; `docs/README.md` |

## What
The four shared building blocks every money task (T003 onward) needs. None of them has business
logic, and there are no schema changes.

Deliverables:
- `internal/platform/money`: `Currency`, `Money` (int64 piasters, unexported fields),
  `New` / `NewPositive`, overflow-checked `Add` / `Sub`, `Format`.
- `internal/platform/id`: UUIDv7 generation.
- `internal/platform/clock`: `Clock` interface, `System` clock (UTC), `clocktest.Fake`, the
  `Africa/Cairo` location and the `CairoDay` business-day helper.
- `internal/platform/database`: `DB` type wrapping the pool, with `InTx` (unit of work): `SET LOCAL`
  lock/statement timeouts, retry on serialization failure / deadlock / lock timeout, and
  `ErrTryAgain` when the retries run out. A retry metric and log.
- Config: `DB_LOCK_TIMEOUT`, `DB_STATEMENT_TIMEOUT`, `DB_TX_MAX_RETRIES`.
- `cmd/wallet` wired to `database.DB` (behaviour unchanged).

## Why
- TDR-003 §3: money is an int64 piaster value type in `internal/platform/money` with validated
  constructors and overflow checks; floats are forbidden (PRD-001 BR-1..BR-4).
- TDR-013: UUIDv7 IDs generated in Go (`platform/id`), and an injected `clock.Clock`.
  Business days use `Africa/Cairo` (PRD-001 §5.6 daily limits, TDR-009 daily settlement).
- TDR-001 §3 / architecture §4: orchestrators run one DB transaction (unit of work) and pass
  `pgx.Tx` to modules. TDR-010 §5: `lock_timeout 2s`, `statement_timeout 5s`, retry the whole
  transaction on `40001` / `40P01` / lock timeout up to 3 times, then `503 TRY_AGAIN`.
- PRD-001 NFR-7: metrics for contention problems.
- T001 left `money`, `id`, `clock` and `InTx` out of scope "for the first task that needs them".
  T003 (ledger) needs all of them.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/platform/money/money.go` | New package (details below) |
| `internal/platform/money/money_test.go` | Table-driven unit tests |
| `internal/platform/id/id.go` | `func New() uuid.UUID` → `uuid.Must(uuid.NewV7())` (panics only if the OS random source fails, the same as `uuid.New`) |
| `internal/platform/id/id_test.go` | Unit tests |
| `internal/platform/clock/clock.go` | `Clock`, `System`, `Cairo`, `CairoDay` (details below) |
| `internal/platform/clock/clocktest/clocktest.go` | `Fake` clock for tests in any package |
| `internal/platform/clock/*_test.go` | Unit tests incl. DST days |
| `internal/platform/database/database.go` | `Open` returns `*DB`; the `DB` type and `TxSettings` |
| `internal/platform/database/tx.go` | `InTx`, retry classification, `ErrTryAgain`, `TxObserver` |
| `internal/platform/database/tx_test.go` | Unit tests for the classification and jitter bounds |
| `internal/platform/database/tx_integration_test.go` | `//go:build integration`, testcontainers Postgres (`databasetest.Shared`) |
| `internal/platform/database/migrate.go` | `NewMigrator` takes `*DB` (or keeps taking the pool through `db.Pool()`; implementer's choice, no behaviour change) |
| `internal/platform/metrics/metrics.go` | Counters `db_tx_retries_total{reason}` and `db_tx_try_again_total`; methods `ObserveTxRetry(reason string)` and `ObserveTxTryAgain()` (so `*metrics.Metrics` satisfies `database.TxObserver`) |
| `internal/platform/config/config.go` | `Wallet` gets `DBLockTimeout` (`DB_LOCK_TIMEOUT`, default `2s`), `DBStatementTimeout` (`DB_STATEMENT_TIMEOUT`, default `5s`), `DBTxMaxRetries` (`DB_TX_MAX_RETRIES`, default `3`). Validation: both timeouts > 0, max retries ≥ 0; an error names the variable |
| `internal/platform/config/config_test.go` | Defaults + invalid values |
| `cmd/wallet/app.go` | `connect` opens `*database.DB` with the settings from config, the logger and `metrics` as observer; the readiness check uses `db.Ping`; `defer db.Close()` |
| `configs/.env.example`, `README.md` | Document the three new variables |
| `.golangci.yml` | No change needed: `forbidigo` already covers `internal/platform/money/`, and `depguard` already covers `internal/platform/**` |

### `money` package
```text
type Currency string
const EGP Currency = "EGP"
func ParseCurrency(s string) (Currency, error)        // only "EGP"; otherwise ErrUnsupportedCurrency

type Money struct { amount int64; currency Currency } // unexported: valid values only via constructors
func New(amount int64, c Currency) (Money, error)        // any sign (balances; system accounts may be negative)
func NewPositive(amount int64, c Currency) (Money, error) // amount > 0, else ErrNotPositive (PRD-001 BR-4)
func (m Money) Amount() int64
func (m Money) Currency() Currency
func (m Money) IsZero() bool
func (m Money) IsNegative() bool
func (m Money) Add(o Money) (Money, error)   // ErrCurrencyMismatch / ErrOverflow, never wraps
func (m Money) Sub(o Money) (Money, error)   // ErrCurrencyMismatch / ErrOverflow
func (m Money) Format() string               // "150.75 EGP", "-0.05 EGP", "0.00 EGP"; integer / and % only
func (m Money) String() string               // = Format (for logs)
```
- Sentinel errors: `ErrUnsupportedCurrency`, `ErrNotPositive`, `ErrCurrencyMismatch`, `ErrOverflow`.
  Messages start with `money:`.
- The zero value `Money{}` has an empty currency. `Add`/`Sub` with it return `ErrCurrencyMismatch`.
  Document that the zero value is not a valid amount.
- Overflow detection is done with integer checks (e.g. `b > 0 && a > math.MaxInt64-b`). `Format`
  must handle `math.MinInt64` correctly (no negation overflow).
- No `float32`/`float64` and no `strconv.ParseFloat` (enforced by `forbidigo`).
- No JSON methods. API encoding (`amount_minor` + `currency`) belongs to the HTTP layer (T004+).

### `id` package
- `New() uuid.UUID` returns a UUIDv7 (`github.com/google/uuid`, already in `go.mod`).
- Nothing else: parsing uses `uuid.Parse` directly.

### `clock` package
```text
type Clock interface { Now() time.Time }
type System struct{}                 // Now() = time.Now().UTC()
var Cairo *time.Location             // loaded once from "Africa/Cairo"
func CairoDay(t time.Time) (start, end time.Time)
```
- `clock` imports `_ "time/tzdata"` itself (with a comment) so `Africa/Cairo` resolves in every
  binary and test that uses the package. Loading failure panics at init: this is a programming
  or build error, not a runtime one. It is the only `init`-time work and it is documented.
- `CairoDay(t)` returns the half-open interval `[start, end)` as **UTC** instants of the
  `Africa/Cairo` calendar day that contains `t`. `start` = the first instant whose Cairo date is that
  day, and `end` = the start of the next Cairo day. It must be correct on DST days (Egypt: the day
  can be 23 h or 25 h). Do **not** rely on `time.Date(…, 0,0,0,0, Cairo)` for a midnight that
  does not exist. Compute it so that the result is exact (for example, take `time.Date` of the
  date and, if the resulting Cairo date differs, step forward to the transition), and prove it
  with tests.
- `clocktest.Fake`: `New(t time.Time) *Fake`, `Now()`, `Set(t)`, `Advance(d)`. It is safe for
  concurrent use (mutex) and always returns UTC.

### `database` unit of work
```text
type TxSettings struct {
    LockTimeout      time.Duration // SET LOCAL lock_timeout
    StatementTimeout time.Duration // SET LOCAL statement_timeout
    MaxRetries       int           // extra attempts after the first (3 ⇒ at most 4 attempts)
}
type TxObserver interface {
    ObserveTxRetry(reason string)  // reason: "serialization_failure" | "deadlock" | "lock_timeout"
    ObserveTxTryAgain()
}
type Options struct { Tx TxSettings; Logger *slog.Logger; Observer TxObserver } // nil Logger/Observer allowed

func Open(ctx context.Context, url string, opts Options) (*DB, error)
func (db *DB) Pool() *pgxpool.Pool
func (db *DB) Ping(ctx context.Context) error
func (db *DB) Close()
func (db *DB) InTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error

var ErrTryAgain = errors.New("database: transaction conflict, try again")
```
`InTx` algorithm (TDR-010 §5):
1. For each attempt: `BEGIN` (default isolation READ COMMITTED, TDR-002), then
   `SELECT set_config('lock_timeout', $1, true), set_config('statement_timeout', $2, true)` with
   millisecond values (`'2000ms'`), then `fn(ctx, tx)`, then `COMMIT`.
2. If `fn` or `COMMIT` returns an error, roll back (rollback errors are logged and not returned).
   Then classify the error with `errors.As(err, *pgconn.PgError)`:
   - `40001` → `serialization_failure`, `40P01` → `deadlock`, `55P03` → `lock_timeout`:
     **retryable**;
   - anything else (including `57014` statement timeout, context errors and business errors from
     `fn`) → return it unchanged (wrapped errors stay matchable with `errors.Is` / `errors.As`).
3. Retryable and attempts left → `Observer.ObserveTxRetry(reason)`, sleep a random 10–50 ms
   (`math/rand/v2`) **while watching `ctx`** (a canceled ctx returns `ctx.Err()` at once),
   then go to the next attempt.
4. Retryable and no attempts left → `Observer.ObserveTxTryAgain()`, a `Warn` log
   (`msg="transaction retries exhausted"`, `reason`, `attempts`), and return an error that
   matches both `ErrTryAgain` and the last `*pgconn.PgError` (`fmt.Errorf("%w: %w", ErrTryAgain, err)`).
5. A panic in `fn` → roll back, then re-panic.
6. Doc comment rules: `fn` may run more than once, so it must have no side effects outside `tx`
   (no network calls, TDR-010 §6). `InTx` must not be nested. The `tx` is not used after `fn` returns.
- `ErrTryAgain` is mapped to `503 TRY_AGAIN` by the HTTP layer of the first endpoint task that
  writes money (T008/T017). This task only defines the sentinel.

### Data (migrations)
None.

### API
None. No endpoint changes. `/readyz` behaves exactly as in T001.

### Behaviour and design notes
- No global mutable state apart from `clock.Cairo` (read-only after init). Everything else is
  wired in `main`.
- The integration tests use their own fresh database (`databasetest.Shared(t).NewDatabase(t)`) and
  create helper tables in the test itself (e.g. `CREATE TABLE t (id int primary key, v int)`).
  They do not touch the migrations.
- Tests that need short timeouts build a `DB` with, for example, `LockTimeout: 200ms` so the suite stays fast.

### Out of scope
- Any business table, sqlc setup (T003), the ledger (T003).
- HTTP error mapping of `ErrTryAgain` (first money endpoint task).
- Strict JSON / `amount_minor` decoding (T004).
- Read-only transactions, other isolation levels, savepoints/nested transactions.
- `platform/queue` (T011).

## Acceptance Criteria
- [ ] AC-1 — Given `NewPositive(0, EGP)` or `NewPositive(-1, EGP)`, then `ErrNotPositive`. Given `NewPositive(1, EGP)`, then a valid value with `Amount()==1`. Given `New(-500, EGP)`, then a valid negative value.
- [ ] AC-2 — Given currency `"USD"`, `""` or `"egp"`, when `ParseCurrency` / `New` is called, then `ErrUnsupportedCurrency`. `"EGP"` is accepted.
- [ ] AC-3 — Given `MaxInt64` + 1 or `MinInt64` − 1 via `Add`/`Sub`, then `ErrOverflow` and no wrapped value. Given values with different currencies (including the zero value), then `ErrCurrencyMismatch`.
- [ ] AC-4 — `Format` gives `15075 → "150.75 EGP"`, `5 → "0.05 EGP"`, `0 → "0.00 EGP"`, `-5 → "-0.05 EGP"`, `-15075 → "-150.75 EGP"`, and `MinInt64` formats without panic.
- [ ] AC-5 — `golangci-lint` passes and reports no float usage in `internal/platform/money`.
- [ ] AC-6 — `id.New()` returns version-7 UUIDs; 1 000 consecutive IDs are unique and sort in non-decreasing time order (`uuid.UUID` byte order).
- [ ] AC-7 — `clock.System{}.Now()` is in UTC. `clocktest.Fake` returns the set time; `Advance` and `Set` work, and parallel `Now`/`Advance` calls pass `-race`.
- [ ] AC-8 — `CairoDay` on a normal day: for `2026-11-05T10:00:00Z` → `[2026-11-04T22:00:00Z, 2026-11-05T22:00:00Z)` (Cairo UTC+2 in winter). An instant at `21:59:59Z` and at `22:00:00Z` fall in different days.
- [ ] AC-9 — `CairoDay` on the 2026 DST-start day (Friday 24 April 2026, midnight → 01:00) → `[2026-04-23T22:00:00Z, 2026-04-24T21:00:00Z)` (23 h). On the DST-end day (Thursday 29 October 2026, 24:00 → 23:00) → `[2026-10-28T21:00:00Z, 2026-10-29T22:00:00Z)` (25 h). *If the embedded tzdata disagrees with these Egyptian rules, stop and ask. Do not change the expected values silently.*
- [ ] AC-10 — Config: the defaults are `DB_LOCK_TIMEOUT=2s`, `DB_STATEMENT_TIMEOUT=5s`, `DB_TX_MAX_RETRIES=3`. A zero or negative timeout, a negative retry count, or a non-number → `wallet serve` exits 1 with an error that names the variable.
- [ ] AC-11 — `InTx` commits when `fn` returns nil (the row is visible afterwards) and rolls back when `fn` returns an error. That error is returned unchanged (`errors.Is` on a sentinel from `fn` works), and nothing is retried.
- [ ] AC-12 — Inside `fn`, `SHOW lock_timeout` / `SHOW statement_timeout` return the configured values. After the transaction, a new connection from the pool still has the server defaults (`SET LOCAL` does not leak).
- [ ] AC-13 — Given a real deadlock between two concurrent `InTx` calls (each locks rows A and B in opposite order), then both calls end successfully (the loser retries). `db_tx_retries_total{reason="deadlock"}` ≥ 1, and the final data is consistent.
- [ ] AC-14 — Given a row held locked by another connection for longer than the lock timeout, when `InTx` tries to lock it, then it makes `MaxRetries+1` attempts and returns an error matching `errors.Is(err, ErrTryAgain)` with SQLSTATE `55P03` reachable through `errors.As`. `db_tx_try_again_total` is incremented and one warn log is written.
- [ ] AC-15 — Given `fn` returns a `40001` error on its first call and succeeds on its second, then `InTx` returns nil, `fn` ran twice, and `db_tx_retries_total{reason="serialization_failure"}` = 1.
- [ ] AC-16 — Given `pg_sleep` longer than the statement timeout, then the `57014` error is returned without any retry.
- [ ] AC-17 — Given ctx is canceled during the wait between retries, then `InTx` returns `context.Canceled` without another attempt.
- [ ] AC-18 — Given `fn` panics, then the transaction is rolled back (no row is written), the panic propagates, and the pool still works for the next `InTx`.
- [ ] AC-19 — After wiring: all T001 tests still pass, `/readyz` behaves as before (database `ok`/`fail`, migrations `ok`/`pending`/`fail`), and `GET /metrics` lists `db_tx_retries_total` and `db_tx_try_again_total`.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | unit | `TestNew_Sign`, `TestNewPositive` (table) |
| AC-2 | unit | `TestParseCurrency` (table) |
| AC-3 | unit | `TestAdd_Overflow`, `TestSub_Overflow`, `TestAddSub_CurrencyMismatch` |
| AC-4 | unit | `TestFormat` (table incl. `math.MinInt64`) |
| AC-5 | lint | `make lint` |
| AC-6 | unit | `TestNew_Version7`, `TestNew_UniqueAndOrdered` |
| AC-7 | unit | `TestSystem_UTC`, `TestFake_SetAdvance`, `TestFake_Concurrent` (`-race`) |
| AC-8 | unit | `TestCairoDay_Winter`, `TestCairoDay_Boundary` |
| AC-9 | unit | `TestCairoDay_DSTStart2026`, `TestCairoDay_DSTEnd2026` |
| AC-10 | unit | `TestConfig_Load` table extended with the three variables; `cmd/wallet` exit-code test for an invalid value |
| AC-11 | integration | `TestInTx_Commit`, `TestInTx_RollbackOnError` |
| AC-12 | integration | `TestInTx_SetsLocalTimeouts` |
| AC-13 | integration | `TestInTx_DeadlockRetried` (two goroutines, barrier so both hold their first lock) |
| AC-14 | integration | `TestInTx_LockTimeoutExhausted_ErrTryAgain` (`LockTimeout: 200ms`, `MaxRetries: 2`, fake observer + captured log) |
| AC-15 | unit | `TestInTx_RetryClassification` (table: 40001/40P01/55P03 retryable; 57014, 23505, `context.Canceled`, plain error not retryable) + integration `TestInTx_SerializationFailureRetried` (`fn` returns `&pgconn.PgError{Code:"40001"}` the first time) |
| AC-16 | integration | `TestInTx_StatementTimeoutNotRetried` |
| AC-17 | integration | `TestInTx_ContextCanceledDuringBackoff` |
| AC-18 | integration | `TestInTx_PanicRollsBack` |
| AC-19 | unit + integration | existing T001 suites; `TestMetrics_Exposed` extended |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Money sign handling | `New` any sign + `NewPositive` / Money always ≥ 0 | Answered | `New` any sign + `NewPositive` |
| 2 | Unit-of-work shape | `database.DB` struct with `InTx` / free function | Answered | `database.DB` struct |
| 3 | Where the tx numbers live; wait between retries | env config + 10–50 ms jitter / constants | Answered | Env config, random 10–50 ms |
| 4 | Cairo business-day helper now or in T008 | now / later | Answered | Now (T002) |
| 5 | `Money` field visibility | unexported + accessors / exported (TDR sketch) | Answered | Unexported + accessors |
| 6 | Retry observability | counter + warn log / later | Answered | Counter + warn log |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Accept PRD-001…004 and TDR-003…011; Q1–Q7 answered with the proposed answers | Yahya | PRDs, TDRs, `docs/README.md` |
| 2026-11-05 | Phase 1 = three flows end to end; reconciliation / fault scenarios / operator API = phase 2; 19-task split approved | Yahya | `engineering/roadmap.md` |
| 2026-11-05 | `discrepancy` module owns `reconciliation_discrepancies` | Yahya | TDR-016 (new), TDR-007, TDR-009, TDR-012, architecture, data model |
| 2026-11-05 | PRD-002 §7 reconciliation query aligned to `GET /v1/operations/{key}` | Yahya | PRD-002 |
| 2026-11-05 | `money.New` any sign, `money.NewPositive` for movements; unexported fields + accessors | Yahya | TDR-003 §3 |
| 2026-11-05 | `database.DB` with `InTx`; `DB_LOCK_TIMEOUT=2s`, `DB_STATEMENT_TIMEOUT=5s`, `DB_TX_MAX_RETRIES=3`, random 10–50 ms between attempts; `55P03` = lock timeout | Yahya | TDR-010 §5 |
| 2026-11-05 | Cairo business-day helper lives in `platform/clock` (T002) | Yahya | this task |
| 2026-11-05 | Retry metrics `db_tx_retries_total{reason}`, `db_tx_try_again_total` + warn log | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Draft | Created by plan-new-task (phase-1 roadmap) |
| 2026-11-05 | Ready | 6 questions answered; doc changes applied |

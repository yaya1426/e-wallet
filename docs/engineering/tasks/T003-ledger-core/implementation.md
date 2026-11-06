# T003 — Implementation

| Field   | Value |
|---------|-------|
| Branch  | `task/T003-ledger-core`, started on the unmerged T002 branch with the user's OK; moved onto `main` @ `366cd0a` after PR #4 merged (identical tree, so nothing changed) |
| Date    | 2026-11-06 |
| Status  | In Review |

## Summary
The double-entry ledger is now the only writer of money, and every later module uses the sqlc
tooling added here.

- **Migration `000002_ledger`** creates the three tables `ledger_accounts`, `journal_entries` and `postings`.
  - **Constraints and indexes:** all the CHECKs, the named constraints
    `ledger_accounts_user_wallet_non_negative` and `journal_entries_transaction_entry_type_key`,
    and indexes `(account_id, created_at)` and `(journal_entry_id)`.
  - **Balance triggers:** two deferred constraint triggers (`postings_balanced`,
    `journal_entries_balanced`, function `ledger_check_entry_balanced`) reject an entry at commit
    if it is unbalanced or has fewer than 2 postings. The error is SQLSTATE 23514 with constraint
    `journal_entry_balanced`. The trigger on `journal_entries` is what catches an entry with
    **no** postings.
  - **Append-only triggers:** they block UPDATE, DELETE and TRUNCATE on entries and postings
    with SQLSTATE 23001.
  - **Seed:** the 3 system accounts, with fixed UUIDs `00000000-0000-7000-8000-00000000000{1,2,3}`.
  - The down file drops everything (local dev only).
- **`internal/ledger`**:
  - `Service.CreateUserAccount` / `LockAccounts` / `Post` / `Balance` / `GetAccountByCode` / `VerifyIntegrity`.
  - Domain types (`AccountID`, `Kind`, `AccountType`, `Direction`, `EntryType`, `Entry`,
    `Posting`, `PostResult`, `IntegrityReport`).
  - Sentinel errors.
  - A `Querier` interface (`pgx.Tx` or the pool).
- **How `Post` works:**
  1. Validate in Go before any SQL.
  2. Insert the entry. A unique violation becomes `ErrDuplicateEntry`.
  3. Apply each posting in account-ID order with one atomic `UPDATE … SET balance = balance + (±1)·amount … RETURNING balance`. The sign comes from the account's normal side, in SQL.
  4. Insert the posting with that `balance_after`.
  - A user-wallet CHECK violation becomes `ErrInsufficientBalance`, and no row becomes `ErrAccountNotFound`.
- **`LockAccounts`:** removes duplicate IDs, sorts them, then `SELECT … ORDER BY id FOR UPDATE`.
- **`internal/ledger/ledgertest.AssertIntegrity(t, db)`:** fails the test and lists every violation.
- **sqlc v1.31.1:**
  - `sqlc.yaml` has one block per module, output to `internal/<m>/internal/sqlcgen`, with `uuid.UUID` and `time.Time` overrides.
  - Makefile targets `tool-sqlc`, `sqlc` and `sqlc-check`; `tools` and `ci` include them.
  - The CI `lint` job runs `make tool-sqlc sqlc-check`.
- **`.golangci.yml`:** generated `sqlcgen` paths are excluded, and a depguard rule `ledger-no-business` is added.
- **Version bump:** four T001 assertions that hard-coded "latest = 1" now expect 2, because this task adds migration 2. They are still exact.

## Files
| Path | Change |
|------|--------|
| `migrations/000002_ledger.up.sql`, `.down.sql` | new |
| `internal/ledger/model.go`, `service.go`, `repository.go`, `queries.sql` | new |
| `internal/ledger/internal/sqlcgen/*` | generated (staged) |
| `internal/ledger/ledgertest/ledgertest.go` | new |
| `internal/ledger/model_test.go`, `ledger_integration_test.go` | new |
| `sqlc.yaml` | new |
| `Makefile`, `.github/workflows/ci.yml`, `.golangci.yml` | sqlc tooling, CI step, lint rules |
| `internal/platform/database/migrate_integration_test.go`, `cmd/wallet/app_integration_test.go` | latest version 1 → 2 |

## Acceptance Criteria → tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestMigration_LedgerSchemaAndSeed`; compose: `migrate status` = 2, then `make migrate-down` → tables gone, `/readyz` 503 pending, then `migrate up` → 2 and 200 | ✅ |
| AC-2 | `TestCreateUserAccount`, `TestCreateUserAccount_Duplicate` (`ErrAccountExists`) | ✅ |
| AC-3 | `TestPost_TopUpSettle` (balances, `BalancesAfter`, versions +1, `balance_after` of both postings, integrity) | ✅ |
| AC-4 | `TestPost_WithdrawalReserve` | ✅ |
| AC-5 | `TestEntryValidate` (13 cases) + `TestPost_InvalidEntryWritesNothing` | ✅ |
| AC-6 | `TestPost_DuplicateEntry` (also: another entry type for the same transaction is allowed) | ✅ |
| AC-7 | `TestPost_InsufficientBalance`, `TestPost_SystemAccountNegativeAllowed` | ✅ |
| AC-8 | `TestTrigger_UnbalancedCommitFails` (unbalanced / one posting / no postings → commit fails 23514) | ✅ |
| AC-9 | `TestTrigger_AppendOnly` (UPDATE/DELETE/TRUNCATE on both tables → 23001) | ✅ |
| AC-10 | `TestPost_ConcurrentCredits` (50 × 100 → 5000) | ✅ |
| AC-11 | `TestPost_OppositeTransfersNoDeadlock` (2 × 50 opposite transfers, no error, balances unchanged) | ✅ |
| AC-12 | `TestVerifyIntegrity_DetectsDrift` (+ `TestVerifyIntegrity_Clean`) | ✅ |
| AC-13 | `TestLockAccounts_NotFound` (+ `TestLockAccounts`, `TestLockAccounts_BlocksConcurrentLocker`) | ✅ |
| AC-14 | manual: `make sqlc-check` → untracked output exit 2, fresh exit 0, `queries.sql` changed exit 2, restored exit 0. CI step added | ✅ |
| AC-15 | `make lint` 0 issues with `sqlcgen` excluded; a throwaway `internal/ledger` file importing a throwaway `internal/wallet` → `depguard: 1`; both files then removed | ✅ |

Also added: `TestIntegrityReport_OK`, `TestCompareIDs_ByteOrder`, `TestPost_UnknownAccount`, `TestGetAccountByCode_NotFound`. The concurrency and trigger tests passed 5 runs in a row (`-count=5`).

## Verification
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `make tidy-check` | ✅ |
| 2 | Format | `make fmt-check` | ✅ |
| 3 | Vet | `make vet` | ✅ |
| 4 | Lint | `make lint` (v2.15.0) | ✅ 0 issues |
| 4b | sqlc | `make sqlc-check` (v1.31.1) | ✅ |
| 5 | Build | `make build` | ✅ |
| 6 | Unit | `go test -race -count=1 ./...` | ✅ 111 tests/sub-tests |
| 7 | Integration | `go test -race -count=1 -tags integration ./...` | ✅ 155 pass, 0 fail |
| 8 | Coverage (unit + integration) | `-cover` | ledger 90.2 %, database 84.8 %, cmd/wallet 86.2 % (`sqlcgen` is generated; `ledgertest` is exercised from `ledger_test`) |
| 9 | Run the service | `POSTGRES_HOST_PORT=5433 make up` | ✅ all healthy (5433 because another project's container holds 5432) |
| 10 | HTTP / DB checks | see below | ✅ |
| 11 | Ledger integrity | SQL of TDR-004 §6 on the compose DB | ✅ totals equal, 0 drifts |
| 12 | Tear down | `docker compose down -v` | ✅ |

## HTTP and database checks (compose)
T003 adds no endpoint, so the checks are on readiness and on the migrated schema.

| Check | Expected | Actual |
|-------|----------|--------|
| `GET /readyz` after `migrate up` | 200, migrations ok | ✅ `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}` |
| `wallet migrate status` | `version=2 dirty=false latest=2` | ✅ |
| Tables | ledger tables exist | ✅ `journal_entries,ledger_accounts,postings,schema_migrations` |
| Seed | 3 system accounts, balance 0 | ✅ `BANK_SETTLEMENT SYSTEM ASSET DEBIT 0`, `WITHDRAWALS_IN_TRANSIT … LIABILITY CREDIT 0`, `SUSPENSE … LIABILITY CREDIT 0` |
| Integrity SQL | totals equal, no drift | ✅ `t | 0` |
| `make migrate-down` | ledger tables removed, version 1 | ✅ `2/d ledger`, 0 tables, `version=1`; `/readyz` → 503 `migrations:"pending"` |
| `migrate up` again | version 2, ready | ✅ `version=2`, `/readyz` 200, 3 system accounts |

Note: `DELETE FROM postings` on the empty live table returns `DELETE 0`, because a row-level
trigger only fires on rows that exist. `TestTrigger_AppendOnly` proves the block on real data.

## Review fixes (round 1 → 2, 2026-11-06)
- **Lock order:** `Post` applies user-wallet postings first and system postings last (`compareLockOrder`, fixed `systemAccountIDs`). An `UPDATE` holds its row lock until commit, so the old ascending-ID order (system accounts first) could deadlock against callers that pre-lock users. This is proven by `TestPost_PreLockedAndUnlockedCreditsNoDeadlock`, which fails with 40P01 on the old order. TDR-010 §3 is clarified.
- **Validation:** `ErrMissingTransactionID`, `ErrMissingWalletID`; unsupported currency is reported as `money.ErrUnsupportedCurrency`.
- **Re-verification:** `make ci` exit 0 (lint 0 issues, sqlc-check ok); 160 tests pass (unit + integration, `-race`); `make up` + `make e2e` ok.

## Deviations from the plan
- **sqlc-check:** it is implemented as "regenerate, then fail on worktree changes or untracked files under `internal/*/internal/sqlcgen/*`". This equals "`git diff --exit-code` on generated dirs" from the task, plus untracked files, so a never-committed output also fails.
- **CI:** the lint job installs sqlc through `make tool-sqlc`, so the version is pinned in one place (the Makefile).
- **No-postings case:** a second deferred trigger on `journal_entries` was added (the task named only the one on `postings`). It catches an entry with zero postings, which the task's rule "at least 2 postings" requires.
- **Extra sentinel:** `ErrInvalidDirection` was added to `Entry.validate` (a direction other than DEBIT/CREDIT). It is part of "validation in Go before SQL".

## Follow-ups (not done)
- T006 adds the FK `journal_entries.transaction_id → transactions` (planned).
- T016 adds `internal/discrepancy` to the platform depguard deny list (TDR-016; already in the ledger rule).

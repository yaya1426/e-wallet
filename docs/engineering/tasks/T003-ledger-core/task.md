# T003 — Ledger core (+ sqlc setup)

| Field        | Value |
|--------------|-------|
| ID           | T003 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T003-ledger-core` |
| Depends on   | T002 |
| PRDs         | PRD-001 BR-6, BR-7, BR-12, BR-13, BR-20; §9.3 |
| TDRs         | TDR-002, TDR-003, TDR-004, TDR-010, TDR-012, TDR-014 |
| Docs changed | `engineering/roadmap.md` (sqlc layout decision) |

## What
The double-entry ledger: the only code that writes money. Also the sqlc tooling that every
later module uses.

Deliverables:
- Migration `000002_ledger`: `ledger_accounts`, `journal_entries`, `postings`, triggers, and
  seeded system accounts.
- `internal/ledger` service: `CreateUserAccount`, `LockAccounts`, `Post`, `Balance`, `GetAccountByCode`, `VerifyIntegrity`.
- `internal/ledger/ledgertest`: an integrity assertion helper for tests in any package.
- sqlc: `sqlc.yaml`, `make sqlc`, `make sqlc-check`, sqlc installed by `make tools`, and a CI check.

## Why
- TDR-004 §1–§6: chart of accounts, postings per flow, storage rules, cached balance, integrity checks.
- PRD-001 BR-12/BR-13: every effect is a balanced journal entry; the ledger is append-only.
- TDR-010 §1–§4: ordered `FOR UPDATE` on user-wallet accounts; system accounts get an atomic update;
  `CHECK (balance >= 0)` on user wallets.
- TDR-012 §3: sqlc for queries (T001 deferred its setup to "the first task with queries").
- PRD-001 §9.3: the integrity check must pass after every test scenario.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000002_ledger.up.sql` / `.down.sql` | Tables, constraints, triggers, seed (below) |
| `internal/ledger/model.go` | `AccountID`, `Account`, `Kind` (`USER_WALLET`, `SYSTEM`), `Direction`, `EntryType`, `Entry`, `Posting`, system codes, errors |
| `internal/ledger/service.go` | `Service` (constructor takes `*database.DB` for reads, `id` gen, `clock.Clock`) |
| `internal/ledger/repository.go` | Wraps the generated sqlc queries |
| `internal/ledger/queries.sql` | sqlc input |
| `internal/ledger/internal/sqlcgen/` | Generated (committed) code; Go `internal` makes it private to `ledger` |
| `internal/ledger/ledgertest/ledgertest.go` | `AssertIntegrity(t, db)` — fails the test if `VerifyIntegrity` reports anything (`//go:build integration` not required; uses `testing.TB`) |
| `internal/ledger/*_test.go`, `*_integration_test.go` | Tests |
| `sqlc.yaml` | Engine `postgresql`, schema `migrations/` (sqlc understands golang-migrate files and ignores `.down.sql`), one `sql:` block per module: `queries: internal/<m>/queries.sql`, `gen.go.out: internal/<m>/internal/sqlcgen`, `package: sqlcgen`, `sql_package: pgx/v5`, `emit_interface: false` |
| `Makefile` | `tools` also installs `sqlc` (pinned `SQLC_VERSION`, latest v1.x on implementation day, recorded in `implementation.md`); `sqlc` (generate); `sqlc-check` (generate + `git diff --exit-code` on generated dirs); `ci` includes `sqlc-check` |
| `.github/workflows/ci.yml` | `lint` job runs `make sqlc-check` (sqlc installed with the pinned version) |
| `.golangci.yml` | Exclude `internal/*/internal/sqlcgen/` from linters (generated). Add the depguard rule `ledger-no-business`: `internal/ledger/**` must not import any other business module |

### Data (migrations)
```sql
ledger_accounts(
  id uuid PK, code text UNIQUE NOT NULL,
  kind text NOT NULL CHECK (kind IN ('USER_WALLET','SYSTEM')),
  type text NOT NULL CHECK (type IN ('ASSET','LIABILITY')),
  normal_side text NOT NULL CHECK (normal_side IN ('DEBIT','CREDIT')),
  currency char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
  balance bigint NOT NULL DEFAULT 0,
  version bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CHECK (kind <> 'USER_WALLET' OR balance >= 0))
journal_entries(
  id uuid PK, transaction_id uuid NOT NULL,          -- FK to transactions added by T006
  entry_type text NOT NULL CHECK (entry_type IN ('TOPUP_SETTLE','WITHDRAWAL_RESERVE',
     'WITHDRAWAL_SETTLE','WITHDRAWAL_REFUND','P2P','ADJUSTMENT')),
  description text NOT NULL DEFAULT '', created_at timestamptz NOT NULL,
  UNIQUE (transaction_id, entry_type))
postings(
  id uuid PK, journal_entry_id uuid NOT NULL REFERENCES journal_entries(id),
  account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  direction text NOT NULL CHECK (direction IN ('DEBIT','CREDIT')),
  amount bigint NOT NULL CHECK (amount > 0),
  currency char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
  balance_after bigint NOT NULL, created_at timestamptz NOT NULL)
INDEX postings (account_id, created_at)
INDEX postings (journal_entry_id)
```
- Trigger `postings_balanced` — `CONSTRAINT TRIGGER … AFTER INSERT ON postings DEFERRABLE
  INITIALLY DEFERRED FOR EACH ROW`: at commit, the posting's entry must have Σdebit = Σcredit and
  at least 2 postings; otherwise `RAISE EXCEPTION` with SQLSTATE `23514` (check_violation).
- Trigger `ledger_append_only` — `BEFORE UPDATE OR DELETE` on `journal_entries` and `postings`
  → `RAISE EXCEPTION` (`TDR-004 §1`). `TRUNCATE` is blocked by a statement-level trigger too.
- Seed (same migration, fixed UUIDs written in the migration so they are stable across
  environments): `BANK_SETTLEMENT` (SYSTEM, ASSET, DEBIT), `WITHDRAWALS_IN_TRANSIT` (SYSTEM,
  LIABILITY, CREDIT), `SUSPENSE` (SYSTEM, LIABILITY, CREDIT).
- DB role grants (TDR-004 §1 mention) are **not** done in phase 1 (single DB user); the triggers
  are the guard. Recorded under Out of scope.
- `.down.sql` drops everything (local dev only).

### API
None.

### Behaviour and design notes
```text
func (s *Service) CreateUserAccount(ctx, tx pgx.Tx, walletID uuid.UUID) (AccountID, error)
    // code "USER_WALLET:<wallet_id>", USER_WALLET / LIABILITY / CREDIT, balance 0
func (s *Service) LockAccounts(ctx, tx, ids []AccountID) (map[AccountID]Account, error)
    // SELECT … WHERE id = ANY($1) ORDER BY id FOR UPDATE; missing id → ErrAccountNotFound
func (s *Service) Post(ctx, tx, e Entry) (PostResult, error)
func (s *Service) Balance(ctx, q Querier, id AccountID) (money.Money, error)
func (s *Service) GetAccountByCode(ctx, q Querier, code string) (Account, error)
func (s *Service) VerifyIntegrity(ctx) (IntegrityReport, error)
```
- `Balance` and `GetAccountByCode` accept either a `pgx.Tx` or run on the pool. Define a small
  `Querier` interface in `ledger` covering `pgx.Tx` and `*pgxpool.Pool` (the sqlc `DBTX`).
- **`Entry`** = `{TransactionID uuid, Type EntryType, Description string, Postings []Posting}`,
  `Posting` = `{AccountID, Direction, Amount money.Money}`.
- **`Post` validation in Go, before any SQL** (each → its own sentinel error):
  ≥ 2 postings (`ErrTooFewPostings`); every amount > 0 (`ErrNonPositiveAmount`); one currency
  (`ErrCurrencyMismatch`); Σdebit = Σcredit computed with `money.Add` (overflow → error)
  (`ErrUnbalanced`); known entry type (`ErrUnknownEntryType`).
- **`Post` steps:**
  1. Insert the journal entry. A unique violation on `(transaction_id, entry_type)` → `ErrDuplicateEntry`
     (this is the ledger-level idempotency guard, TDR-005 L4; callers treat it as "already applied").
  2. For each posting, in account-ID order: change the account with one statement,
     `UPDATE ledger_accounts SET balance = balance + $delta, version = version + 1, updated_at = $now
     WHERE id = $id RETURNING balance, kind`. `delta` is signed by the normal side: for a
     credit-normal account, credit is + and debit is −; for a debit-normal account it is the
     opposite (TDR-004 §4). Then insert the posting with `balance_after` = the returned balance.
  3. A `23514` from the user-wallet `CHECK` → `ErrInsufficientBalance` (a last-line guard; the
     callers check first under lock).
  4. Return `PostResult{EntryID, BalancesAfter map[AccountID]int64}`.
- **Locking contract (doc comment):** callers that check a user-wallet balance must call
  `LockAccounts` first, in the same tx (TDR-010 §1). System accounts are never passed to
  `LockAccounts`. They are only changed by the atomic `UPDATE` in `Post` (TDR-010 §3).
- **`VerifyIntegrity`** (TDR-004 §6 items 1–2, read-only, on the pool): (a) Σ all debit
  postings = Σ all credit postings; (b) for every account, cached `balance` = the balance computed
  from its postings; (c) every journal entry is balanced. It returns a report listing each
  violation. Item 3 (`WITHDRAWALS_IN_TRANSIT` vs transactions) needs the transaction module and
  comes in phase 2 (`ledger_integrity` job).
- All amounts are `int64` piasters / `money.Money` (TDR-003). No floats.
- IDs come from `id.New()`. Timestamps come from the injected clock (UTC).

### Out of scope
- FK `journal_entries.transaction_id → transactions` (T006).
- Operator `ADJUSTMENT` posting API (phase 2). The entry type already exists.
- DB role grants / a separate DB user for ledger writes.
- Statement / history read APIs.

## Acceptance Criteria
- [ ] AC-1 — Given `migrate up`, then the three tables exist, the 3 system accounts exist with balance 0, and `migrate status` shows version 2. `make migrate-down` reverses it.
- [ ] AC-2 — Given `CreateUserAccount(walletID)`, then an account `USER_WALLET:<walletID>` exists (USER_WALLET/LIABILITY/CREDIT, balance 0). A second call with the same wallet ID fails (unique code).
- [ ] AC-3 — Given a balanced entry Dr `BANK_SETTLEMENT` 1000 / Cr user 1000, when posted, then the user balance = 1000, `BANK_SETTLEMENT` = 1000 (debit-normal), each posting's `balance_after` is right, `version` went up by 1 on both, and `VerifyIntegrity` is clean.
- [ ] AC-4 — Given an entry Dr user 1000 / Cr `WITHDRAWALS_IN_TRANSIT` 1000 on a user with balance 1000, then user = 0 and in-transit = 1000.
- [ ] AC-5 — Given an unbalanced entry, < 2 postings, an amount ≤ 0, or mixed currency, then `Post` returns the matching sentinel and nothing is written.
- [ ] AC-6 — Given the same `(transaction_id, entry_type)` posted twice, then the second returns `ErrDuplicateEntry` and the balances changed only once.
- [ ] AC-7 — Given a debit that would make a user wallet negative, then `ErrInsufficientBalance` and the tx rolls back. System accounts may go negative without error.
- [ ] AC-8 — Given raw SQL inserting an unbalanced set of postings, when the tx commits, then the commit fails (deferred trigger).
- [ ] AC-9 — Given raw SQL `UPDATE`/`DELETE`/`TRUNCATE` on `postings` or `journal_entries`, then it fails.
- [ ] AC-10 — Given 50 goroutines each posting +100 into the same user account (each in its own `InTx` with `LockAccounts`), then the final balance = 5000 and `VerifyIntegrity` is clean.
- [ ] AC-11 — Given two goroutines repeatedly posting A→B and B→A (P2P entries) with `LockAccounts([A,B])`, then no error reaches the caller (ordered locks; any retry happens inside `InTx`), and the final balances are correct.
- [ ] AC-12 — Given a cached `ledger_accounts.balance` changed by a raw `UPDATE` (the append-only trigger covers entries and postings only), then `VerifyIntegrity` reports that account.
- [ ] AC-13 — `LockAccounts` with an unknown ID → `ErrAccountNotFound`.
- [ ] AC-14 — `make sqlc-check` passes on a clean tree and fails when `queries.sql` is changed without regenerating. CI runs it.
- [ ] AC-15 — `golangci-lint` passes. The generated code is excluded, and `internal/ledger` importing another business module is a depguard error (proved once with a throwaway file, recorded in `implementation.md`).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | integration | `TestMigration_LedgerSchemaAndSeed` |
| AC-2 | integration | `TestCreateUserAccount`, `TestCreateUserAccount_Duplicate` |
| AC-3, AC-4 | integration | `TestPost_TopUpSettle`, `TestPost_WithdrawalReserve` |
| AC-5 | unit | `TestEntryValidate` (table) |
| AC-6 | integration | `TestPost_DuplicateEntry` |
| AC-7 | integration | `TestPost_InsufficientBalance`, `TestPost_SystemAccountNegativeAllowed` |
| AC-8, AC-9 | integration | `TestTrigger_UnbalancedCommitFails`, `TestTrigger_AppendOnly` |
| AC-10 | integration | `TestPost_ConcurrentCredits` |
| AC-11 | integration | `TestPost_OppositeTransfersNoDeadlock` |
| AC-12 | integration | `TestVerifyIntegrity_DetectsDrift` |
| AC-13 | integration | `TestLockAccounts_NotFound` |
| AC-14 | CI / manual | `make sqlc-check` |
| AC-15 | lint / manual | `make lint`, throwaway import |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | FK `journal_entries → transactions` timing | T006 migration / now | Answered | Added by T006 |
| 2 | `ADJUSTMENT` now or phase 2 | type now, API phase 2 / all now | Answered | Type now, API phase 2 |
| 3 | sqlc generated code location | per module `internal/<m>/internal/sqlcgen` / shared package | Answered | Per module, Go-internal |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | FK to `transactions` added by T006; `ADJUSTMENT` allowed but no API until phase 2 | Yahya | roadmap |
| 2026-11-05 | sqlc: per-module `queries.sql` → `internal/<m>/internal/sqlcgen`, schema from `migrations/`, `make sqlc-check` in CI | Yahya | roadmap, this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

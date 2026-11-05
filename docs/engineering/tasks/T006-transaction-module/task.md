# T006 — Transaction module (records, state machine, sums)

| Field        | Value |
|--------------|-------|
| ID           | T006 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T006-transaction-module` |
| Depends on   | T003, T004 |
| PRDs         | PRD-001 BR-11, BR-14, BR-15, §5.6; PRD-002 §3.5; PRD-003 WD-BR-7 |
| TDRs         | TDR-004 §1, TDR-005 L2, TDR-013, architecture §5.5 |
| Docs changed | `engineering/roadmap.md` (limits split) |

## What
The business record of every user action and its state machine. No HTTP in this task (history
endpoints are T010).

Deliverables:
- `transactions` table + FK from `journal_entries.transaction_id`.
- `internal/transaction` service: `Create`, `Get`, `GetForUpdate`, `Transition`,
  `SetBankReference` (via `Transition` patch), `FindByBankRef`, `SumOutgoingForDay`, `SumPendingTopUps`.

## Why
PRD-001 BR-11 (one transaction per user action), BR-14/15 (statuses, terminal states),
TDR-004 §1 (transaction vs ledger), TDR-005 L2 (`UNIQUE (initiator_user_id, idempotency_key)`
forever), architecture §5.5 (state machine). The limit sums are needed by T008/T017/T018. Limits
split (decided 2026-11-05): the sums live here, and the rules live in `wallet`.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000005_transactions.up/down.sql` | Table, indexes, FK on `journal_entries` |
| `internal/transaction/model.go` | `Type`, `Status`, `Transaction`, `NewTransaction`, `Patch`, transition table, errors |
| `internal/transaction/service.go`, `repository.go`, `queries.sql` | Service + sqlc |
| `.golangci.yml` | depguard: `transaction` imports only platform |

### Data (migrations)
```sql
transactions(
  id uuid PK,
  type text NOT NULL CHECK (type IN ('TOPUP','WITHDRAWAL','P2P')),
  status text NOT NULL CHECK (status IN ('PENDING','PROCESSING','COMPLETED','FAILED')),
  initiator_user_id uuid NOT NULL REFERENCES users(id),
  source_wallet_id uuid NULL REFERENCES wallets(id),
  destination_wallet_id uuid NULL REFERENCES wallets(id),
  amount bigint NOT NULL CHECK (amount > 0),
  currency char(3) NOT NULL DEFAULT 'EGP' CHECK (currency='EGP'),
  idempotency_key text NOT NULL,
  bank_reference text NULL UNIQUE,
  bank_account_snapshot jsonb NULL,
  note text NULL CHECK (note IS NULL OR char_length(note) <= 140),
  failure_code text NULL, failure_message text NULL,
  last_reconciled_at timestamptz NULL,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, completed_at timestamptz NULL,
  UNIQUE (initiator_user_id, idempotency_key),
  CHECK ((type='TOPUP' AND destination_wallet_id IS NOT NULL AND source_wallet_id IS NULL)
      OR (type='WITHDRAWAL' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NULL)
      OR (type='P2P' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NOT NULL
          AND source_wallet_id <> destination_wallet_id)))
INDEX (status, updated_at) WHERE status IN ('PENDING','PROCESSING')
INDEX (source_wallet_id, created_at DESC, id DESC)
INDEX (destination_wallet_id, created_at DESC, id DESC)
ALTER TABLE journal_entries ADD FOREIGN KEY (transaction_id) REFERENCES transactions(id)
```

### API
None.

### Behaviour and design notes
- **Allowed transitions** (architecture §5.5): `PENDING→PROCESSING`, `PENDING→FAILED`,
  `PROCESSING→COMPLETED`, `PROCESSING→FAILED`. `Create` accepts `PENDING` (TOPUP/WITHDRAWAL) or
  `COMPLETED` (P2P only); anything else → `ErrInvalidInitialStatus`.
- `Transition(ctx, tx, id, from, to, Patch{BankReference *string, FailureCode, FailureMessage *string})`:
  1. `(from, to)` not in the table → `ErrInvalidTransition` (no SQL).
  2. `UPDATE transactions SET status=$to, … , updated_at=$now, completed_at = CASE WHEN $to IN
     terminal THEN $now END WHERE id=$id AND status=$from RETURNING *`.
  3. 0 rows → read the row: missing → `ErrNotFound`, otherwise → `ErrStatusConflict{Current}`.
  4. A unique violation on `bank_reference` → `ErrDuplicateBankReference`.
- `Create`: a unique violation on `(initiator_user_id, idempotency_key)` → `ErrDuplicateIdempotencyKey`
  (TDR-005 L2: even after the 24 h key is gone, a late retry cannot create a second transaction).
- `GetForUpdate(ctx, tx, id)` = `SELECT … FOR UPDATE`. `FindByBankRef(ctx, q, ref)`.
- **Sums** (amounts as `int64` piasters; `0` when there is nothing):
  - `SumOutgoingForDay(ctx, tx, walletID, start, end)` = Σ amount where `source_wallet_id = walletID`
    AND `created_at ∈ [start, end)` AND ((`type='WITHDRAWAL'` AND status IN
    (`PENDING`,`PROCESSING`,`COMPLETED`)) OR (`type='P2P'` AND status='COMPLETED')). `start/end`
    come from `clock.CairoDay` (PRD-001 §5.6, PRD-003 WD-BR-7).
  - `SumPendingTopUps(ctx, tx, walletID)` = Σ amount of `TOPUP` with `destination_wallet_id = walletID`
    and status IN (`PENDING`,`PROCESSING`) (PRD-002 §3.5).
  - Callers run these after locking the wallet's ledger account, so the sum cannot change under them.
- `bank_account_snapshot` is set at `Create`: `{ "iban": "...", "holder_name": "..." }`.
- Time from the injected clock. IDs from `id.New()` (the transaction ID is also the bank idempotency key, TDR-013).

### Out of scope
HTTP endpoints (T010), the limit rules (T008), reconciliation fields being used (phase 2).

## Acceptance Criteria
- [ ] AC-1 — `migrate up` applies version 5. A journal entry with an unknown `transaction_id` is now rejected by the FK.
- [ ] AC-2 — `Create` of a TOPUP `PENDING`, a WITHDRAWAL `PENDING` and a P2P `COMPLETED` (with `completed_at` set) succeeds. A P2P with `PENDING`, a TOPUP with a source wallet, or P2P to the same wallet fails.
- [ ] AC-3 — Same user + same idempotency key twice → `ErrDuplicateIdempotencyKey`. Different users with the same key → both fine.
- [ ] AC-4 — Each allowed transition works and sets `updated_at` (and `completed_at` for terminal ones). Each not-allowed pair (incl. from `COMPLETED`/`FAILED`) → `ErrInvalidTransition`.
- [ ] AC-5 — `Transition(from=PENDING)` on a row that is already `PROCESSING` → `ErrStatusConflict{Current: PROCESSING}` and no change. An unknown ID → `ErrNotFound`.
- [ ] AC-6 — A patch with a `bank_reference` already used by another transaction → `ErrDuplicateBankReference`. `FindByBankRef` finds the row.
- [ ] AC-7 — `SumOutgoingForDay` counts withdrawals PENDING/PROCESSING/COMPLETED + P2P COMPLETED of that Cairo day, and excludes FAILED, other wallets, incoming P2P, and rows from the day before/after (rows at 21:59:59Z and 22:00:00Z in winter).
- [ ] AC-8 — `SumPendingTopUps` counts only PENDING/PROCESSING top-ups to that wallet.
- [ ] AC-9 — Two concurrent `Transition(PENDING→PROCESSING)` on the same row → exactly one succeeds, and the other gets `ErrStatusConflict`.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | integration | `TestMigration_TransactionsFK` |
| AC-2 | unit + integration | `TestNewTransaction_Validate` (table), `TestCreate_ByType` |
| AC-3 | integration | `TestCreate_DuplicateIdempotencyKey` |
| AC-4 | unit + integration | `TestTransitionTable`, `TestTransition_Allowed` |
| AC-5, AC-9 | integration | `TestTransition_StatusConflict`, `TestTransition_Concurrent` |
| AC-6 | integration | `TestTransition_DuplicateBankReference`, `TestFindByBankRef` |
| AC-7, AC-8 | integration | `TestSumOutgoingForDay` (table with fake clock), `TestSumPendingTopUps` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Where limit sums and rules live | sums in transaction, rules in wallet / all in transaction | Answered | Sums here, rules in wallet |
| 2 | Transition semantics | `WHERE status=from` + typed errors / other | Answered | As described |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Sums in `transaction`, limit config + checks in `wallet`, combined by orchestrators | Yahya | roadmap |
| 2026-11-05 | `Transition` with `WHERE status=from`; `ErrInvalidTransition` / `ErrStatusConflict` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

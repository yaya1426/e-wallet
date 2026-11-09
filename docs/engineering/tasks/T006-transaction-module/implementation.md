# T006 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T006-transaction-module`, stacked on T005 (PR #7, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **Migration `000005_transactions`**: the `transactions` table exactly as planned. Constraints are named, so the code maps them to errors:
  - `transactions_initiator_idempotency_key`, kept forever (TDR-005 L2);
  - `transactions_bank_reference_key`;
  - `transactions_wallets_by_type`.
  It also adds the 3 indexes and the FK `journal_entries_transaction_id_fkey` → `transactions`.
- **`internal/transaction`**:
  - **Model:** `Type`, `Status` (`Terminal`), the transition table + `CanTransition`, `Transaction`, `New` (validated: initial status by type, wallets by type, initiator, positive EGP amount, key, note ≤ 140 characters), `Patch`, `BankAccountSnapshot`. Errors: `ErrInvalidInitialStatus`, `ErrInvalidWallets`, `ErrInvalidTransaction`, `ErrInvalidTransition`, `ErrStatusConflict` + `*StatusConflictError{Expected, Current}`, `ErrNotFound`, `ErrDuplicateIdempotencyKey`, `ErrDuplicateBankReference`.
  - **Service:** `Create`, `Get`, `GetForUpdate`, `FindByBankRef`, `Transition` (`UPDATE … WHERE status = from`; on 0 rows it reads the row → `ErrNotFound` or a conflict; terminal statuses set `completed_at`), `SumOutgoingForDay`, `SumPendingTopUps`. Times are truncated to µs.
- **sqlc**: a `transaction` block with overrides for nullable uuid → `uuid.NullUUID`, nullable text/timestamptz → pointers.
- **depguard**: `transaction-deps` (platform only, non-test files); probed.
- **Existing tests adapted to the new FK** (every journal entry now needs a transaction):
  - The ledger integration fixture seeds a user and wallet with SQL; `f.txnID(t)` inserts a pending top-up (the ledger package may not import other modules).
  - The wallet API test creates a real top-up through `transaction.Service`.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestMigration_TransactionsFK` (version ≥ 5; entry with an unknown transaction → 23503 on `journal_entries_transaction_id_fkey`); compose DB at version 5 with the FK | ✅ |
| AC-2 | `TestNewTransaction_Validate` (table); `TestCreate_ByType` (TOPUP/WITHDRAWAL PENDING, P2P COMPLETED with `completed_at`, returned = stored, P2P PENDING rejected); `TestCreate_DBChecksBackTheRules` | ✅ |
| AC-3 | `TestCreate_DuplicateIdempotencyKey` (same user → error; another user, same key → ok) | ✅ |
| AC-4 | `TestTransitionTable` (all 16 pairs); `TestTransition_Allowed` (3 paths, `updated_at`/`completed_at`); `TestTransition_NotAllowed`; `TestTransition_PatchFields` | ✅ |
| AC-5 | `TestTransition_StatusConflict` (Current = PROCESSING, row unchanged; unknown ID → `ErrNotFound`) | ✅ |
| AC-6 | `TestTransition_DuplicateBankReference`, `TestFindByBankRef` | ✅ |
| AC-7 | `TestSumOutgoingForDay` (Cairo day 2026-11-10: rows at 21:59:59Z and 22:00:00Z; PENDING/PROCESSING/COMPLETED withdrawals + outgoing P2P counted; FAILED, incoming P2P, other wallet, top-ups, next day excluded) | ✅ |
| AC-8 | `TestSumPendingTopUps` | ✅ |
| AC-9 | `TestTransition_Concurrent` (exactly one ok, one `ErrStatusConflict`) | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, fmt, vet, lint, sqlc-check, build, unit, integration) | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 292 passed, 0 failed |
| Coverage `internal/transaction` | 89.7% |
| depguard probe (`transaction` → `wallet`) | reported; probe removed |
| `make up` + `make e2e` | ✅; DB at version 5, FK present |
| HTTP | not applicable: no endpoints in this task (T010) |

## Deviations from the plan
- **`StatusConflictError` carries `Expected` too:** the task named `ErrStatusConflict{Current}`. Implemented as the sentinel `ErrStatusConflict` (for `errors.Is`) plus `*StatusConflictError{Expected, Current}` (for `errors.As`).
- **`ErrInvalidWallets` / `ErrInvalidTransaction`:** these separate errors cover the other invalid inputs of `Create` that the task listed only as "fails".
- **Sums return `int64` piasters**, as the task says (not `money.Money`).

## Follow-ups (not done)
- None.

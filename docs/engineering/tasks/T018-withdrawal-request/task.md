# T018 — Withdrawal request (reserve + payout handler)

| Field        | Value |
|--------------|-------|
| ID           | T018 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T018-withdrawal-request` |
| Depends on   | T017 |
| PRDs         | PRD-003 §2 WD-1, 4, 5, §3, §4, §5 WD-BR-1, 4, 5, 6, 7, 8, §6, §8 AC-1, 2, 6 |
| TDRs         | TDR-004 §3, TDR-005, TDR-006 §3, TDR-010 |
| Docs changed | — |

## What
`POST /v1/withdrawals` reserves the money at once (a real ledger entry into
`WITHDRAWALS_IN_TRANSIT`) and stores an outbox event. The outbox worker asks the bank to pay out.
A synchronous bank rejection refunds the money in the same tx as the `FAILED` transition.

## Why
PRD-003 WD-BR-1 (reserve by ledger entry), WD-BR-4 (fail + refund only when the bank confirms
failure), WD-BR-5/6 (retry technical errors; no new payout after a rejection), WD-BR-7 (daily
limit), WD-BR-8 (frozen in flight); TDR-010 (lock before balance check); TDR-004 §3 postings.
The check order was decided 2026-11-05 (same as top-up, then funds, then daily limit).

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/payment/withdrawal.go` | `RequestWithdrawal` |
| `internal/payment/withdrawal_handler.go` | Outbox handler `withdrawal.requested` |
| `internal/payment/refund.go` | `failAndRefund(ctx, tx, txn, code, msg)` shared with T019 |
| `internal/payment/http.go` | `POST /v1/withdrawals` |
| `internal/wallet/limits.go` | `CheckWithdrawalAmount` |
| `cmd/wallet/app.go` | Register handler, route |
| `api/openapi/wallet.yaml` | Endpoint |

### Data (migrations)
None.

### API
`POST /v1/withdrawals` — same headers/body as top-up. `202 { transaction_id, type: "WITHDRAWAL", status: "PENDING", amount_minor, currency, created_at }`.

Errors in **check order**:
| # | Check | HTTP | Code |
|---|-------|------|------|
| 1–5 | token, decode, key, amount ≤ 0, currency | 401/400 | as in T017 |
| — | *idempotency `Begin`* | | |
| 6 | Wallet `FROZEN` | 403 | `WALLET_FROZEN` |
| 7 | No linked bank account | 422 | `BANK_ACCOUNT_REQUIRED` |
| 8 | Outside `[WITHDRAWAL_MIN, WITHDRAWAL_MAX]` | 422 | `AMOUNT_OUT_OF_RANGE` |
| 9 | Balance < amount (under lock) | 422 | `INSUFFICIENT_FUNDS` |
| 10 | Today's outgoing + amount > `DAILY_OUTGOING` | 422 | `DAILY_LIMIT_EXCEEDED` |
| — | DB conflict | 503 | `TRY_AGAIN` |

### Behaviour and design notes
**Request** (one `InTx`):
1. `idempotency.Begin`.
2. Frozen (6), bank account (7), range (8).
3. `ledger.LockAccounts([wallet account])`; balance (9); `transaction.SumOutgoingForDay(CairoDay(now))` (10).
4. `transaction.Create(WITHDRAWAL, PENDING, source = wallet, snapshot)`.
5. `ledger.Post(WITHDRAWAL_RESERVE: Dr USER_WALLET / Cr WITHDRAWALS_IN_TRANSIT)`.
6. `outbox.Enqueue(withdrawal.requested, {"transaction_id"})`; `idempotency.Complete(202, …)`.
After the commit: `outbox.Wake()`. The available balance is already lower (WD-1, BR-9).

**Outbox handler `withdrawal.requested`** (no tx open during the bank call):
1. Load the transaction; status ≠ `PENDING` → `Done`.
2. `bank.CreatePayout{IdempotencyKey: txnID, Amount, IBAN, HolderName from the snapshot}`.
3. `Acceptance` → `InTx: Transition(PENDING→PROCESSING, bank_reference)`; conflict → `Done`.
4. `*RejectedError` → `InTx`: lock the txn, and if it is still `PENDING`, `failAndRefund` =
   `Transition(PENDING→FAILED, bank code/message)` + `ledger.Post(WITHDRAWAL_REFUND: Dr
   WITHDRAWALS_IN_TRANSIT / Cr USER_WALLET)` in the same tx. The refund is credited even if the
   wallet is frozen or above the max balance (WD-BR-8). Then `Done`.
5. `ErrTemporary` → `Retry`. **Never refund on a timeout** (WD-BR-4). `DEAD` keeps the money
   reserved until phase 2 `dead_outbox` asks the bank.
- No automatic new payout after a rejection (WD-BR-6).

### Out of scope
Payout webhooks (T019), `dead_outbox`/stuck handling (phase 2).

## Acceptance Criteria
- [x] AC-1 — Balance 50 000, withdraw 20 000 → `202 PENDING`; balance 30 000; in-transit +20 000; one `WITHDRAWAL_RESERVE` entry; one `withdrawal.requested` event (PRD-003 AC-1).
- [x] AC-2 — Each error row → its status/code; nothing written.
- [x] AC-3 — 10 parallel withdrawals of 10 000 on a 50 000 balance (different keys) → exactly 5 `202`, 5 `INSUFFICIENT_FUNDS`, final balance 0, never negative (PRD-003 AC-2).
- [x] AC-4 — Daily limit: with 5 000 000 already sent today (P2P + withdrawals), a withdrawal of 1 100 000 → `DAILY_LIMIT_EXCEEDED`; a FAILED withdrawal from today does not count.
- [x] AC-5 — Same key twice → one withdrawal, one reserve; the second gets the replay (WD-4).
- [x] AC-6 — Handler + fake gateway: accept → `PROCESSING`; sync rejection → `FAILED` + one `WITHDRAWAL_REFUND`, balance restored, in-transit back to 0 for it; temporary ×3 then accept → paid once with the same key, no refund (PRD-003 AC-6).
- [x] AC-7 — Handler after the webhook already moved the transaction → `Done`, nothing changed.
- [x] AC-8 — `WITHDRAWALS_IN_TRANSIT` balance = Σ amounts of withdrawals in PENDING/PROCESSING after every test (data-model invariant 5, asserted by a test helper in `payment`).
- [x] AC-9 — Ledger integrity holds after every scenario.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2, AC-5 | integration HTTP | `TestWithdrawal_Accepted`, `TestWithdrawal_Errors` (table), `TestWithdrawal_Idempotent` |
| AC-3 | integration | `TestWithdrawal_ParallelNeverNegative` |
| AC-4 | integration | `TestWithdrawal_DailyLimit` |
| AC-6, AC-7 | integration (fake gateway) | `TestWithdrawalHandler_*` |
| AC-8, AC-9 | integration | invariant helpers in cleanup |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Check order | same as top-up, then funds, then daily limit / other | Answered | As listed |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Withdrawal check order = top-up order, then `INSUFFICIENT_FUNDS`, then `DAILY_LIMIT_EXCEEDED`; sync rejection → FAILED + refund in one tx | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | Implementation started (on top of T017, PR #19) |
| 2026-11-09 | In Review | Implemented; verification passed (implementation.md) |
| 2026-11-09 | Approved | Review round 1: 0 Blocker, 0 Major, 0 Minor, 2 Nits (review.md) |
| 2026-11-09 | Done | Finalized — PR opened |

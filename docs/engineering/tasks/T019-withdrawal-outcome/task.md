# T019 — Withdrawal outcome (settle / refund)

| Field        | Value |
|--------------|-------|
| ID           | T019 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T019-withdrawal-outcome` |
| Depends on   | T016, T018 |
| PRDs         | PRD-003 WD-2, WD-3, WD-BR-2, 3, 4, 8, 9, §7 (discrepancy rows), §8 AC-3, 4, 5 |
| TDRs         | TDR-007 §3, TDR-004 §3, TDR-009 §3, TDR-016 |
| Docs changed | — |

## What
`ApplyBankOutcome` learns payouts: a confirmed payout settles the reservation, and a failed one
refunds it, exactly once. **After this task, withdrawal works end to end.**

## Why
PRD-003 WD-BR-2 (Dr in-transit / Cr settlement), WD-BR-3 (refund entry), WD-BR-4 (refund only on a
confirmed failure), WD-BR-8 (frozen in flight), WD-BR-9 (amount mismatch → discrepancy), §7 (paid
but ours FAILED / failed but ours COMPLETED → critical, operator only).

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/payment/outcome.go` | `payout.*` events supported in `HandleBankEvent`; WITHDRAWAL branch of `ApplyBankOutcome` |
| `internal/payment/refund.go` | Reuse `failAndRefund` from T018 (PROCESSING→FAILED variant) |

### Data (migrations)
None.

### API
None (results visible through `GET /v1/transactions/{id}` and `GET /v1/me/wallet`).

### Behaviour and design notes
`ApplyBankOutcome` for a `WITHDRAWAL` (txn locked by the caller). It uses the same rule table as
T016 for kind mismatch, contradicting terminal state, agreeing terminal state, amount mismatch and
`PENDING` → `PROCESSING` first. Then:
| State + outcome | Action |
|-----------------|--------|
| `PROCESSING` + `SUCCEEDED` | `ledger.Post(WITHDRAWAL_SETTLE: Dr WITHDRAWALS_IN_TRANSIT / Cr BANK_SETTLEMENT)` + `Transition(→COMPLETED)`. The wallet balance does not change again |
| `PROCESSING` + `FAILED` | `ledger.Post(WITHDRAWAL_REFUND: Dr WITHDRAWALS_IN_TRANSIT / Cr USER_WALLET)` + `Transition(→FAILED, bank code/message)`, credited even if the wallet is frozen / above max |
| `COMPLETED` + `FAILED`, or `FAILED` + `SUCCEEDED` | `STATUS_MISMATCH` CRITICAL (the second one means the user may have been paid twice), no money change |
| Amount ≠ requested | `AMOUNT_MISMATCH` CRITICAL, no posting, the money stays reserved |
- A payout event for a TOPUP → `STATUS_MISMATCH` (kind mismatch).
- Remove the `ErrUnsupportedEvent` path that T016 had for `payout.*`.

### Out of scope
Reconciliation-driven outcomes and `STUCK` (phase 2).

## Acceptance Criteria
- [x] AC-1 — `PROCESSING` withdrawal of 20 000 + `payout.succeeded` → `COMPLETED`; one `WITHDRAWAL_SETTLE`; in-transit −20 000; `BANK_SETTLEMENT` −20 000; wallet unchanged (PRD-003 AC-3).
- [x] AC-2 — `payout.failed` (`ACCOUNT_CLOSED`) → `FAILED` with that code; one `WITHDRAWAL_REFUND`; wallet +20 000; in-transit −20 000 (AC-4).
- [x] AC-3 — Repeated identical outcome → no-op; contradicting outcome after terminal → one `STATUS_MISMATCH`, no money change (AC-5).
- [x] AC-4 — Amount mismatch → `AMOUNT_MISMATCH`, still `PROCESSING`, money stays reserved.
- [x] AC-5 — Frozen wallet + `payout.failed` → refund still credited.
- [x] AC-6 — `PENDING` withdrawal (no reference yet) + `payout.succeeded` by idempotency key → `PROCESSING` then `COMPLETED`.
- [x] AC-7 — The in-transit invariant (T018 AC-8) and ledger integrity hold after every scenario.
- [x] AC-8 — End to end on compose: top up, then withdraw with `…0000` → `COMPLETED`; with `…0001` → `FAILED ACCOUNT_CLOSED` and the balance is back; with `…0002` → `FAILED INVALID_ACCOUNT` and the balance is back.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-6 | integration | `TestApplyBankOutcome_Withdrawal` (table state × outcome) |
| AC-7 | integration | invariant helpers in cleanup |
| AC-8 | manual on compose (recorded in `implementation.md`), automated in T020 | curl walkthrough |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| — | None — all behaviour comes from PRD-003 and the T016 rules | | | |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Same rule table as T016 applied to payouts | planning (from PRD-003) | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; no open questions |
| 2026-11-09 | In Progress | Implementation started (on top of T018, PR #20) |
| 2026-11-09 | In Review | Implemented; verification passed (implementation.md) |
| 2026-11-09 | Approved | Review round 1: 0 Blocker, 0 Major, 0 Minor, 1 Nit (review.md) |
| 2026-11-09 | Done | Finalized — PR opened |

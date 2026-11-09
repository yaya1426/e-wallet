# Implementation — T019 Withdrawal outcome (settle / refund)

## Summary
`ApplyBankOutcome` now applies payout outcomes. It uses the same rule table as T016: kind, reference, terminal agreement or contradiction, amount, and PENDING → PROCESSING first. Then for a WITHDRAWAL:
- `SUCCEEDED` → `settleWithdrawal`: `WITHDRAWAL_SETTLE` (Dr `WITHDRAWALS_IN_TRANSIT` / Cr `BANK_SETTLEMENT`) and `PROCESSING → COMPLETED`. The wallet is unchanged, because the reserve already debited it.
- `FAILED` → `failAndRefund` (from T018): `PROCESSING → FAILED` with the bank's code and message, and `WITHDRAWAL_REFUND` (Dr in-transit / Cr wallet), credited even if the wallet is frozen.
- `COMPLETED` + failed, or `FAILED` + paid → one CRITICAL `STATUS_MISMATCH`, no money change. The second case means the user may have been paid twice.
- Amount mismatch → `AMOUNT_MISMATCH`; the withdrawal stays PROCESSING and the money stays reserved.

The `ErrUnsupportedOutcome` paths for payouts in `HandleBankEvent` and `ApplyBankOutcome` were removed, along with the error itself. **Withdrawal now works end to end.**

## Files
| Path | Change |
|------|--------|
| `internal/payment/outcome.go` | Payout events accepted; the final switch covers type × status; `settleWithdrawal` |
| `internal/payment/service.go` | `ErrUnsupportedOutcome` removed |
| `internal/payment/withdrawal_outcome_integration_test.go` (new) | Payout outcome tests |
| `internal/payment/outcome_integration_test.go` | The two T016 "payout waits for T019" checks now expect the outcome to be applied |

`refund.go` is reused unchanged: `failAndRefund` transitions from the current status, here PROCESSING.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestApplyBankOutcome_Withdrawal_Paid` (COMPLETED; reserve + settle entries; in-transit 0; `BANK_SETTLEMENT` −20 000; wallet unchanged) | ✅ |
| AC-2 | `TestApplyBankOutcome_Withdrawal_FailedRefunds` (FAILED `ACCOUNT_CLOSED`; reserve + refund; wallet +20 000; in-transit 0) | ✅ |
| AC-3 | `TestApplyBankOutcome_Withdrawal_Terminal` (paid ×3 → settled once; then failed ×2 → one `STATUS_MISMATCH`, no refund. Failed ×2 → refunded once; then paid → one `STATUS_MISMATCH`, refund kept) | ✅ |
| AC-4 | `TestApplyBankOutcome_Withdrawal_AmountMismatch` (still PROCESSING, in-transit 20 000, one entry) | ✅ |
| AC-5 | `TestApplyBankOutcome_Withdrawal_RefundToFrozenWallet` | ✅ |
| AC-6 | `TestHandleBankEvent_PayoutBeforeBankReference` (`payout.succeeded` and `payout.failed`, matched by idempotency key → PROCESSING → final, reference stored) | ✅ |
| AC-7 | `paymenttest.AssertInTransit` and `ledgertest.AssertIntegrity` in every payment test's cleanup; live SQL | ✅ |
| AC-8 | Compose walkthrough (below) | ✅ |

Also tested: `TestHandleBankEvent_PayoutForTopUp` (kind mismatch → `STATUS_MISMATCH`, no money).

## Verification
| # | Step | Result |
|---|------|--------|
| 1–5 | `make ci` | ✅ 0 lint issues |
| 6–7 | Full suite `-race -tags integration` | ✅ 653 passed, 0 failed |
| 8 | Coverage | `internal/payment/...` 85.5% (across packages) |
| 9–10 | Compose + live script | below |
| 11 | Ledger integrity + invariant 5 (SQL) | ✅ 0 unbalanced, 0 drift; in-transit 0 = in-flight 0; `BANK_SETTLEMENT` 270 000 = 3 × 100 000 top-ups − 30 000 paid out |
| 12 | `make e2e && make down` | ✅ |

### AC-8 walkthrough (compose, real banksim)
Each user was funded with a real top-up of 100 000 (…0000), then switched the linked account and withdrew 30 000.

| IBAN | Response | Balance right after | Final | Balance | Replay |
|------|----------|---------------------|-------|---------|--------|
| …0000 | 202 PENDING | 70 000 | COMPLETED (2.1 s) | 70 000 | same id |
| …0001 | 202 PENDING | 70 000 | FAILED `ACCOUNT_CLOSED` (2.1 s) | 100 000 (refunded) | same id |
| …0002 | 202 PENDING | 70 000 | FAILED `INVALID_ACCOUNT` (rejected at once) | 100 000 (refunded) | same id |

Afterwards:
- Inbox: `debit.succeeded`, `payout.succeeded` and `payout.failed` all DONE at attempt 1.
- 0 discrepancies; 0 ERROR log lines.
- Out of range → 422 `AMOUNT_OUT_OF_RANGE`; more than the balance → 422 `INSUFFICIENT_FUNDS`.

## Deviations
None.

## Follow-ups
- Phase 2: reconciliation-driven outcomes (`bank.Operation.Outcome()` → `ApplyBankOutcome`) and stuck transactions.

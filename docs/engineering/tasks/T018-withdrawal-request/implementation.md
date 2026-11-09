# Implementation — T018 Withdrawal request (reserve + payout handler)

## Summary
`POST /v1/withdrawals` accepts a withdrawal in one unit of work:
- **Order:** idempotency `Begin` → checks 6–10 under the wallet account lock → `WITHDRAWAL PENDING` transaction with the bank account snapshot → `WITHDRAWAL_RESERVE` (Dr user wallet / Cr `WITHDRAWALS_IN_TRANSIT`) → outbox `withdrawal.requested` → idempotency `Complete`; then `outbox.Wake()`.
- **Balance:** the available balance drops at once (WD-1).

The outbox handler calls `bank.CreatePayout`, with the transaction ID as the idempotency key. Then:
- accepted → PROCESSING with the bank reference;
- synchronous rejection → in one transaction, lock the withdrawal and, if it is still PENDING, `failAndRefund`: FAILED with the bank's code and message, plus `WITHDRAWAL_REFUND` (Dr in-transit / Cr wallet, credited even if frozen);
- temporary failure → Retry, never a refund (WD-BR-4);
- transaction already moved on → Done.

**Refactor (also fixes T017 review Minor #1):**
- `payment/request.go` now holds what top-up and withdrawal share: the idempotent unit of work (`request`), checks 6–7 (`checkedWallet`), `enqueueRequested`, and one outbox handler (`deliverRequested`) parameterised by a `bankFlow` (type, bank call, rejection).
- The business errors stored for the key come from the single `businessErrors` table in `http.go` (`isBusinessError`), so rendering and storing cannot drift apart.
- `TopUpInput` is now `RequestInput`.

## Files
| Path | Change |
|------|--------|
| `internal/payment/request.go` (new) | Shared request unit of work, wallet checks, enqueue, `deliverRequested` + `bankFlow` |
| `internal/payment/topup.go` | `RequestTopUp` / `deliverTopUpRequested` on the shared code; `topup_handler.go` removed |
| `internal/payment/withdrawal.go` (new) | `RequestWithdrawal`, `deliverWithdrawalRequested`, `EventWithdrawalRequested` |
| `internal/payment/refund.go` (new) | `failAndRefund` (shared with T019) |
| `internal/payment/http.go` | `POST /v1/withdrawals`; shared `handleRequest`; `INSUFFICIENT_FUNDS` and `DAILY_LIMIT_EXCEEDED` in the business table; `isBusinessError` |
| `internal/payment/service.go` | Registers `withdrawal.requested` in `OutboxHandlers` |
| `internal/payment/paymenttest/paymenttest.go` (new) | `AssertInTransit`: `WITHDRAWALS_IN_TRANSIT` balance = Σ withdrawals PENDING/PROCESSING (data-model invariant 5) |
| `api/openapi/wallet.yaml` (+ test) | `POST /v1/withdrawals` |
| Tests | `internal/payment/withdrawal_handler_integration_test.go`, `cmd/wallet/withdrawal_integration_test.go`. The payment fixture's withdrawals are now funded and reserved like real ones, and `AssertInTransit` runs after every payment test. |

`wallet.CheckWithdrawalAmount` and `CheckDailyOutgoing` already existed (T008). `cmd/wallet` needed no change: payment's `OutboxHandlers()` and `Routes` already register everything.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestWithdrawal_Accepted` (202; balance 50 000 → 30 000; in-transit 20 000; one `WITHDRAWAL_RESERVE`; one `withdrawal.requested`; source wallet, snapshot) | ✅ |
| AC-2 | `TestWithdrawal_Errors` (12 rows in check order; no transaction, event or journal entry; the whole balance can then be withdrawn) | ✅ |
| AC-3 | `TestWithdrawal_ParallelNeverNegative` (10 × 10 000 on 50 000 → exactly 5 × 202 + 5 × `INSUFFICIENT_FUNDS`; balance 0; in-transit 50 000) | ✅ |
| AC-4 | `TestWithdrawal_DailyLimit` (4 000 000 P2P + 1 000 000 withdrawal; a 500 000 withdrawal rejected by the bank → FAILED and not counted; 1 100 000 → `DAILY_LIMIT_EXCEEDED`; 1 000 000 accepted at exactly 6 000 000) | ✅ |
| AC-5 | `TestWithdrawal_Idempotent` (replay, one withdrawal and one reserve; stored `INSUFFICIENT_FUNDS` replayed after funding) | ✅ |
| AC-6 | `TestWithdrawalHandler_Accept`, `_RejectRefunds` (FAILED + refund, balance and in-transit restored; redelivery: no call, no second refund), `_TemporaryThenAccept` (4 calls with the same key, never refunded meanwhile), `TestWithdrawal_DeliveredToTheBank` (real wiring) | ✅ |
| AC-7 | `TestWithdrawalHandler_AlreadyProgressed` (moved on before the call → no call; rejection arriving after it moved on → Done, no refund) | ✅ |
| AC-8 | `paymenttest.AssertInTransit` in the cleanup of every payment test and every withdrawal API test; live SQL check | ✅ |
| AC-9 | `ledgertest.AssertIntegrity` in every fixture's cleanup; live SQL check | ✅ |

Also tested:
- `TestWithdrawalHandler_RefundToFrozenWallet` (WD-BR-8).
- `TestWithdrawalHandler_WrongType`.

## Verification
| # | Step | Result |
|---|------|--------|
| 1–5 | `make ci` | ✅ 0 lint issues (a gofmt fix in a test file was needed once) |
| 6–7 | Full suite `-race -tags integration` | ✅ 642 passed, 0 failed |
| 8 | Coverage | `internal/payment/...` 86.1% (its tests + cmd/wallet, `-coverpkg`) |
| 9–10 | Compose + live script | below |
| 11 | Ledger integrity + invariant 5 (SQL) | ✅ 0 unbalanced, 0 drift; in-transit 60 000 = in-flight 60 000 |
| 12 | `make e2e && make down` | ✅ |

### Live checks (compose, real banksim)
Each user was funded by a real top-up (…0000), then their linked account was switched, and they withdrew 30 000 of 100 000.

| IBAN | Response | Balance right after | After ~3 s | Replay |
|------|----------|---------------------|------------|--------|
| …0000 | 202 PENDING | 70 000 | PROCESSING (payout accepted) | same id |
| …0001 | 202 PENDING | 70 000 | PROCESSING (payout accepted; the bank fails it later by webhook) | same id |
| …0002 | 202 PENDING | 70 000 | FAILED `INVALID_ACCOUNT`, balance 100 000 (refunded) | same id |

Other live results:
- 10 000 000 → 422 `AMOUNT_OUT_OF_RANGE`; 2 000 000 with a balance of 100 000 → 422 `INSUFFICIENT_FUNDS`.
- Outbox: 3 `topup.requested` + 3 `withdrawal.requested`, all DONE.
- Inbox: the `payout.succeeded` and `payout.failed` webhooks stay PENDING and retry. This is expected: payout outcomes return `ErrUnsupportedOutcome` until T019, so those withdrawals stay PROCESSING with the money reserved.
- Wallet logs: no ERROR lines.

## Deviations
- The top-up code was refactored into shared helpers, with no behaviour change; all T017 tests still pass unchanged.
- `failAndRefund` transitions from the transaction's current status, so T019 can reuse it from PROCESSING. The sync-rejection caller locks the row and requires PENDING.

## Follow-ups
- T019: apply `payout.succeeded` / `payout.failed` (settle / `failAndRefund`).

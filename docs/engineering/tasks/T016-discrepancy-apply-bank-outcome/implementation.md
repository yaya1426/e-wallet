# Implementation — T016 Discrepancy module + ApplyBankOutcome (debits)

## Summary
- **`internal/discrepancy`** (new, owns `reconciliation_discrepancies`):
  - `Record(ctx, tx, New)` inserts an OPEN row in the caller's transaction. While one is OPEN, the same (transaction, kind) is not recorded again, through the partial unique index and `ON CONFLICT DO NOTHING`.
  - `List(ctx, Filter{Status, Limit})` returns rows newest first.
  - It imports only `platform` and never changes money or status.
- **`internal/payment`** (new orchestrator):
  - `ApplyBankOutcome(ctx, tx, txnID, bank.Outcome)` locks the transaction, then applies the rules of the task's table in this order:
    1. final outcome required;
    2. kind vs type;
    3. payout → `ErrUnsupportedOutcome` (T019);
    4. bank reference mismatch;
    5. terminal: no-op if it agrees, `STATUS_MISMATCH` if it contradicts;
    6. amount → `AMOUNT_MISMATCH`;
    7. PENDING → PROCESSING with the reference;
    8. success → `TOPUP_SETTLE` (Dr BANK_SETTLEMENT / Cr USER_WALLET) and COMPLETED; failure → FAILED with the bank's code and message.

    A discrepancy returns nil, so the event is DONE and an operator takes over.
  - `HandleBankEvent` implements `webhook.EventHandler`:
    - payout events → `ErrUnsupportedOutcome` (retried until T019);
    - it finds the transaction by bank reference, else by idempotency key, else `webhook.ErrUnknownTransaction`;
    - it then calls `ApplyBankOutcome`.
- **`bank.Outcome`** (+ `Final()`, `Operation.Outcome()`) is shared by the webhook path and phase-2 reconciliation.
- **`webhook`**: on the last attempt that still returns `ErrUnknownTransaction`, it records `UNKNOWN_BANK_EVENT` (WARNING, no transaction; details = provider, event id/type, reference, key, amount) in its own `InTx`. The runner then marks the event DEAD. `NewService` now requires a discrepancy `Recorder`.
- **`cmd/wallet`** builds `discrepancy` and `payment` and calls `inbox.SetHandler(payment)`, so the inbox now runs ("inbox processing started").

## Files
| Path | Change |
|------|--------|
| `migrations/000009_discrepancies.up/down.sql` | Table, partial unique `(transaction_id, kind) WHERE OPEN AND transaction_id IS NOT NULL`, `(status, created_at)` index |
| `internal/discrepancy/{model,service}.go`, `queries.sql`, sqlcgen; `sqlc.yaml` | Module |
| `internal/bank/gateway.go` | `Outcome`, `Final`, `Operation.Outcome` |
| `internal/payment/{service,outcome}.go` | Module |
| `internal/webhook/{model,service}.go` | `Recorder`, `maxAttempts`, `recordUnknown` |
| `cmd/wallet/app.go` | Wiring |
| `.golangci.yml` | `discrepancy-deps` (platform only), `payment-deps` (denies transfer, reconciliation, banksim), platform rule denies `discrepancy`, forbidigo money scope includes `discrepancy`, webhook comment |
| Tests | `internal/discrepancy/discrepancy_integration_test.go`, `internal/payment/outcome_integration_test.go`, `internal/webhook/*_test.go` (recorder + `TestInbox_DeadUnknownRecordsDiscrepancy`), `cmd/wallet/app_integration_test.go` (`TestServe_Inbox` now expects processing) |

`internal/wallet` already had `GetByID`, so no change was needed there.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestApplyBankOutcome_TopUp_Succeeded` (COMPLETED + `completed_at`, wallet and BANK_SETTLEMENT +50 000, one `TOPUP_SETTLE`) | ✅ |
| AC-2 | `TestApplyBankOutcome_TopUp_Failed` (FAILED, bank's code/message, no entry, balances 0) | ✅ |
| AC-3 | `TestHandleBankEvent_RepeatedOutcomes` (5 events, new ids → applied once), `TestApplyBankOutcome_TopUp_Terminal` (same outcome = no-op) | ✅ |
| AC-4 | `TestApplyBankOutcome_TopUp_Terminal` (COMPLETED + failed ×2 → one CRITICAL `STATUS_MISMATCH`; FAILED + succeeded → recorded, not credited) | ✅ |
| AC-5 | `TestApplyBankOutcome_TopUp_AmountMismatch` (49 999 → `AMOUNT_MISMATCH`, stays PROCESSING; PENDING stays PENDING) | ✅ |
| AC-6 | `TestHandleBankEvent_BeforeBankReference`, `TestApplyBankOutcome_TopUp_Pending` | ✅ |
| AC-7 | `TestApplyBankOutcome_TopUp_CreditsDespiteFreezeAndMaxBalance` (FROZEN wallet, 15 000 000 > max) | ✅ |
| AC-8 | `TestInbox_DeadUnknownRecordsDiscrepancy` (nothing before the last attempt; then one WARNING row, no transaction, event facts), `TestHandleBankEvent_Lookup` | ✅ |
| AC-9 | `TestApplyBankOutcome_Mismatches` ("debit outcome for a withdrawal"; also payout-for-top-up, other reference) | ✅ |
| AC-10 | `TestDiscrepancy_RecordDedupAndList` (dedupe, other kind/transaction, no-transaction rows never deduped, newest first, status filter, limit, re-open after resolve), `TestDiscrepancy_RecordedOnlyIfTheCallerCommits`, `TestDiscrepancy_Invalid` | ✅ |
| AC-11 | `ledgertest.AssertIntegrity` in every payment test's cleanup; live SQL check | ✅ |

Also tested:
- `TestApplyBankOutcome_Errors`: not final → `ErrNotFinal`; payout → `ErrUnsupportedOutcome`; unknown ID.
- `TestInbox_AppliesDebitThroughPayment`: a signed webhook goes through the inbox and payment to COMPLETED, with the inbox row DONE.

## Verification
| # | Step | Result |
|---|------|--------|
| 1–5 | `make ci` (tidy, gofmt, vet, golangci-lint, sqlc-check, build, unit + integration `-race`) | ✅ 0 lint issues |
| 6–7 | Full suite `-race -tags integration` | ✅ 586 tests (incl. `TestHandleBankEvent_ConcurrentOutcomes`, run ×3). In the first of four full runs one test failed and its name was not captured. Three more full runs and four repeated runs of each touched package (`webhook`, `payment`, `discrepancy`, `cmd/wallet`, `banksim`, `bank/...`) all passed, so it is recorded as an unidentified flake. |
| 8 | Coverage | `payment` 90.0%, `discrepancy` 95.2%, `webhook` 97.1% |
| 9 | Run | `POSTGRES_HOST_PORT=5433 make up` → `/readyz` ok |
| 10 | Live checks | below |
| 11 | Ledger integrity (SQL: unbalanced entries, account drift) | ✅ 0 / 0; BANK_SETTLEMENT = 50 000 = the credited top-up |
| 12 | `make e2e && make down` | ✅ |

### Live checks (compose; no top-up API until T017, so the top-ups were inserted with SQL)
The user was registered through the API. Three PENDING top-ups were then sent to banksim with `Idempotency-Key = reference = transaction ID`. The simulator's webhooks went through the inbox to payment:

| Debit | Expected | Actual |
|-------|----------|--------|
| 50 000, IBAN …0000 | COMPLETED, reference stored, balance +50 000 | COMPLETED, has reference; `GET /v1/me/wallet` → `available_balance_minor: 50000` |
| 20 000, IBAN …0001 | FAILED `INSUFFICIENT_FUNDS` | FAILED `INSUFFICIENT_FUNDS`; `GET /v1/me/transactions` shows it |
| top-up 30 000, debit 29 999 | `AMOUNT_MISMATCH` CRITICAL, stays PENDING, no money | as expected (30000 vs 29999) |
| inbox | 3 × DONE, attempt 1 | 3 × DONE, attempt 1 |

## Deviations
- `payment` imports `webhook`: to implement `webhook.EventHandler`, it must use `webhook.BankEvent`. The task's list of allowed imports for `payment` does not name `webhook`; the import creates no cycle, because `webhook` imports only `platform` and `discrepancy`. `payment-deps` therefore denies only `transfer`, `reconciliation` and `banksim`.
- `ApplyBankOutcome` takes the row lock itself (`GetForUpdate`) instead of relying on the caller, so reconciliation cannot forget it. Locking again inside the same transaction is a no-op.
- Additional rule: an event whose bank reference belongs to transaction A while its idempotency key names B → CRITICAL `STATUS_MISMATCH` on A, nothing applied (never guess).
- Payout outcomes on a WITHDRAWAL return `ErrUnsupportedOutcome` (retried) until T019, as the task says for payout events.

## Follow-ups
- The unidentified flaky failure in one full run. If it shows up again in CI, read the failing test name from the CI log.
- T019: payout outcomes in `ApplyBankOutcome`.

# Implementation — T017 Top-up request (API + outbox handler)

## Summary
`POST /v1/topups` accepts a top-up in one unit of work:
- **Order:** idempotency `Begin` → checks 6–9 → `TOPUP PENDING` transaction with the bank account snapshot → outbox `topup.requested` → idempotency `Complete`; then `outbox.Wake()` after the commit.
- **Ledger:** no entry until the bank confirms (TU-BR-1).
- **Limit check:** the wallet's ledger account is locked during the maximum-balance check, so two parallel top-ups cannot both pass.

The outbox handler calls `bank.CreateDebit` with the transaction ID as the idempotency key. No DB transaction is open during the call. Then:
- accepted → `PENDING → PROCESSING` with the bank reference;
- rejected → `FAILED` with the bank's code and message;
- temporary failure → `Retry`;
- transaction already moved on (webhook first), before or during the call → `Done`, nothing changed.

With T015/T016, top-up now works end to end on compose with banksim.

`cmd/wallet` now builds the real bank client (`banksim.New`) in `serve`. `newAPI` takes the gateway, so tests pass `banktest.New()`. Payment's outbox handlers are registered while wiring.

## Files
| Path | Change |
|------|--------|
| `internal/payment/service.go` | `Deps` / `NewService(Deps)`, `OutboxHandlers()`, `Response`, `Renderer` |
| `internal/payment/topup.go` | `RequestTopUp`, `EventTopUpRequested`, `ErrBankAccountRequired` |
| `internal/payment/topup_handler.go` | `deliverTopUpRequested`, `finishRequest` |
| `internal/payment/http.go` | `POST /v1/topups`, `CodeBankAccountRequired`, business error table, 202 body |
| `internal/payment/outcome.go` | `ApplyBankOutcome` no longer stores an empty bank reference on PENDING → PROCESSING (hardening found while writing the race test) |
| `cmd/wallet/app.go` | Bank client in `serve`; `newAPI(…, gateway)`; payment wiring, outbox registration, route |
| `api/openapi/wallet.yaml` (+ test) | `POST /v1/topups`, `AmountRequest`, `AcceptedTransaction`; `BANK_ACCOUNT_REQUIRED` and `WALLET_LIMIT_EXCEEDED` in the catalogue |
| Tests | `internal/payment/topup_handler_integration_test.go`, `cmd/wallet/topup_integration_test.go`; fixtures updated (`apiFixture.api`, `apiFixture.bank`; payment `Deps`) |

`wallet/limits.go` already had `CheckTopUpAmount` and `CheckTopUpBalance` (the task calls the latter `CheckMaxBalance`). They were reused unchanged.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestTopUp_Accepted` (202 body; `TOPUP PENDING` with destination wallet, no source, snapshot; one `topup.requested` event with `transaction_id`; balance 0; no journal entry) | ✅ |
| AC-2 | `TestTopUp_Errors` (15 rows, including order checks such as frozen beating no bank account and range; nothing created; boundaries 1 000 / 3 000 000 / exactly the max balance accepted) | ✅ |
| AC-3 | `TestTopUp_Idempotent` (replay header and same body, one transaction and event, `IDEMPOTENCY_KEY_REUSED`, stored 403 replayed after unfreezing), `TestTopUp_ParallelSameKey` (one transaction, one event, one bank call; the other gets 202 replay or 409) | ✅ |
| AC-4 | `TestTopUpHandler_Accept` (key = transaction ID, IBAN/holder from the snapshot), `TestTopUpHandler_Reject` (`INVALID_ACCOUNT` → FAILED), `TestTopUp_DeliveredToTheBank` (real wiring: outbox → fake bank → PROCESSING, event DONE) | ✅ |
| AC-5 | `TestTopUpHandler_TemporaryThenAccept` (3 × Retry, then Done; 4 calls, same key; PROCESSING) | ✅ |
| AC-6 | `TestTopUpHandler_AlreadyProgressed` (before the call: no bank call; during the call: the webhook completes it and the transition conflict is Done; credited once) | ✅ |
| AC-7 | `TestTopUp_MaxBalanceWithPending` (9 000 000 + 900 000 + 200 000 → 422; + 100 000 accepted), `TestTopUp_ParallelMaxBalance` (two parallel 150 000 on 9 800 000 → exactly one 202) | ✅ |
| AC-8 | `TestTopUp_Errors` (frozen → 403), `TestTopUpHandler_FrozenAfterAcceptance` (bank still called) | ✅ |
| AC-9 | Compose walkthrough (below) | ✅ |
| AC-10 | `ledgertest.AssertIntegrity` in every fixture's cleanup; live SQL check | ✅ |

## Verification
| # | Step | Result |
|---|------|--------|
| 1–5 | `make ci` (tidy, gofmt, vet, golangci-lint, sqlc-check, build, unit + integration `-race`) | ✅ 0 issues |
| 6–7 | Full suite `-race -tags integration` | ✅ 616 passed, 0 failed |
| 8 | Coverage | `internal/payment` 87.1% (its tests + cmd/wallet, `-coverpkg`); `cmd/wallet` 85.4% |
| 9 | `POSTGRES_HOST_PORT=5433 make up` | ✅ ready |
| 10 | HTTP checks | below |
| 11 | Ledger integrity (SQL) | ✅ 0 unbalanced entries, 0 drift |
| 12 | `make e2e && make down` | ✅ |

### AC-9 walkthrough (compose, real banksim; script in Python because zsh mangled the curl flags)
Each run: register → link IBAN → `POST /v1/topups` 50 000 → poll `GET /v1/transactions/{id}` → `GET /v1/me/wallet` → repeat with the same key.

| IBAN | Response | Final status | Time | Balance | Replay |
|------|----------|--------------|------|---------|--------|
| …0000 | 202 PENDING | COMPLETED | 2.1 s | 50 000 | 202, replayed, same id |
| …0001 | 202 PENDING | FAILED `INSUFFICIENT_FUNDS` | 2.1 s | 0 | 202, replayed, same id |
| …0002 | 202 PENDING | FAILED `INVALID_ACCOUNT` | 0.2 s (rejected at once by the bank) | 0 | 202, replayed, same id |

Live error checks: no token 401 `UNAUTHENTICATED`; no key 400 `IDEMPOTENCY_KEY_REQUIRED`; amount 0 400 `INVALID_AMOUNT`; USD 400 `UNSUPPORTED_CURRENCY`; malformed 400 `MALFORMED_REQUEST`; no bank account 422 `BANK_ACCOUNT_REQUIRED`; 999 and 3 000 001 → 422 `AMOUNT_OUT_OF_RANGE`.

Outbox afterwards: 3 × `topup.requested` DONE at attempt 1. Inbox: `debit.succeeded` and `debit.failed` DONE. Wallet logs: 0 ERROR lines.

## Deviations
- `payment.NewService` now takes a `Deps` struct, because the service grew from 6 to 12 collaborators. The T016 tests were updated accordingly.
- The outbox handler finishes as `Fail` (no retry) for a payload that cannot be decoded, an unknown transaction, or a transaction that is not a top-up with a snapshot. Retrying these can never succeed.

## Follow-ups
- Phase 2: reconciliation of `DEAD` `topup.requested` events and of stuck `PENDING` top-ups (TU-BR-7).

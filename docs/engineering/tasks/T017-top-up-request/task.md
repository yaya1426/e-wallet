# T017 — Top-up request (API + outbox handler)

| Field        | Value |
|--------------|-------|
| ID           | T017 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T017-top-up-request` |
| Depends on   | T005, T007, T008 (limits, 503 mapping), T012, T013, T016 |
| PRDs         | PRD-002 §2 TU-1..5, §3, §4, §5 TU-BR-1, 5, 8, §6, §8 AC-1, 5, 7 |
| TDRs         | TDR-005, TDR-006 §1–3, TDR-008 §1, TDR-010 |
| Docs changed | — |

## What
`POST /v1/topups` accepts a top-up and stores it together with an outbox event. The outbox worker
asks the bank to pull the money. **After this task, top-up works end to end on compose with banksim**
(bank result through the webhook from T015/T016).

## Why
PRD-002 §3 preconditions, §4 flow, §6 contract and errors, TU-BR-5 (retry technical errors with the
same key, a business rejection fails at once), TU-BR-8 (frozen); TDR-006 §3 (outcome → event state);
TDR-005 L3 (bank key = transaction ID). Decided 2026-11-05: lock the wallet account during the
max-balance check, and the check order below.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/payment/topup.go` | `RequestTopUp` use case |
| `internal/payment/topup_handler.go` | Outbox handler `topup.requested` |
| `internal/payment/http.go` | `POST /v1/topups` |
| `internal/wallet/limits.go` | `CheckTopUpAmount`, `CheckMaxBalance(balance, pending, amount)` |
| `cmd/wallet/app.go` | Register the handler; route; pass `outbox.Wake` |
| `api/openapi/wallet.yaml` | Endpoint |

### Data (migrations)
None.

### API
`POST /v1/topups` — headers `Authorization`, `Idempotency-Key`; body `{ "amount_minor": 50000, "currency": "EGP" }`.
`202 { "transaction_id", "type": "TOPUP", "status": "PENDING", "amount_minor", "currency", "created_at" }`.

Errors in **check order**:
| # | Check | HTTP | Code |
|---|-------|------|------|
| 1 | Token | 401 | `UNAUTHENTICATED` |
| 2 | Body decodes | 400 | `MALFORMED_REQUEST` |
| 3 | Key | 400 | `IDEMPOTENCY_KEY_REQUIRED` / `IDEMPOTENCY_KEY_INVALID` |
| 4 | `amount_minor` ≤ 0 | 400 | `INVALID_AMOUNT` |
| 5 | currency ≠ EGP | 400 | `UNSUPPORTED_CURRENCY` |
| — | *idempotency `Begin`* | 422 / 409 / replay | `IDEMPOTENCY_KEY_REUSED` / `REQUEST_IN_PROGRESS` |
| 6 | Wallet `FROZEN` | 403 | `WALLET_FROZEN` |
| 7 | No linked bank account | 422 | `BANK_ACCOUNT_REQUIRED` |
| 8 | Outside `[TOPUP_MIN, TOPUP_MAX]` | 422 | `AMOUNT_OUT_OF_RANGE` |
| 9 | balance + pending/processing top-ups + amount > `MAX_BALANCE` | 422 | `WALLET_LIMIT_EXCEEDED` |
| — | DB conflict | 503 | `TRY_AGAIN` |

Checks 6–9 are stored by idempotency `Complete` and replayed for the same key.

### Behaviour and design notes
**Request** (one `InTx`):
1. `idempotency.Begin` → replay if any.
2. `wallet.GetByUserID` → frozen (6). `identity.GetBankAccount` → (7). Range (8).
3. `ledger.LockAccounts([wallet account])` — serializes this user's top-up checks — then read the
   balance and `transaction.SumPendingTopUps` (9).
4. `transaction.Create(TOPUP, PENDING, destination = wallet, bank_account_snapshot = {iban, holder_name})`.
5. `outbox.Enqueue(topup.requested, payload {"transaction_id"})`.
6. `idempotency.Complete(202, body, &txnID)`.
After the commit: `outbox.Wake()`. No ledger entry (TU-BR-1).

**Outbox handler `topup.requested`** (no DB tx is open during the bank call, TDR-010 §6):
1. Load the transaction (plain read). Status ≠ `PENDING` → `Done` (it already moved on, e.g. the
   webhook arrived first, T016).
2. `bank.CreateDebit{IdempotencyKey: txnID, Amount, IBAN, HolderName from the snapshot}`.
3. Result:
   - `Acceptance` → `InTx`: `Transition(PENDING→PROCESSING, bank_reference)`. `ErrStatusConflict`
     (already PROCESSING/terminal) → `Done` without change. Then `Done`.
   - `*RejectedError` → `InTx`: `Transition(PENDING→FAILED, failure_code/message = the bank's)`.
     Conflict → `Done`. Then `Done` (the event ends `DONE`, TDR-006 §3).
   - `ErrTemporary` (or a DB error) → `Retry(err)`. After `OUTBOX_MAX_ATTEMPTS` → `DEAD`. The
     transaction stays `PENDING` (phase 2 `dead_outbox` resolves it, TU-BR-7).
- A frozen wallet does not stop the handler (TU-BR-8: already-accepted top-ups continue).

### Out of scope
Reconciliation of `DEAD`/stuck top-ups (phase 2), withdrawal (T018).

## Acceptance Criteria
- [ ] AC-1 — Valid request → `202 PENDING`; a `TOPUP PENDING` row with the bank snapshot; one `topup.requested` outbox event; balance unchanged; no journal entry (PRD-002 AC-1).
- [ ] AC-2 — Each row of the error table → its status + code, and no transaction or outbox row is created.
- [ ] AC-3 — Same key + body twice (sequentially) → the same response, one transaction, one event. Two parallel with the same key → one transaction, one bank call; the other gets `409` or the replay (PRD-002 AC-5).
- [ ] AC-4 — With a fake gateway: acceptance → `PROCESSING` + `bank_reference`; rejection `INVALID_ACCOUNT` → `FAILED` with that code; event `DONE` in both.
- [ ] AC-5 — Fake gateway returns `ErrTemporary` 3 times then accepts → 4 calls, all with the same idempotency key, transaction `PROCESSING` (PRD-002 AC-7).
- [ ] AC-6 — If the webhook (T016) already moved the transaction, the handler's transition conflict → `Done`, no error, no double change.
- [ ] AC-7 — Max balance: balance 9 000 000 + pending 900 000 + request 200 000 > 10 000 000 → `WALLET_LIMIT_EXCEEDED`. Two parallel top-ups that together would exceed it → exactly one accepted.
- [ ] AC-8 — Frozen wallet → `403`; freezing after acceptance does not stop the bank call.
- [ ] AC-9 — End to end on compose (scripted in the implementation run, later in T020): register, link `…0000`, top up 50 000 → within ~5 s `GET /v1/transactions/{id}` is `COMPLETED` and the balance is 50 000. With `…0001` → `FAILED INSUFFICIENT_FUNDS`; with `…0002` → `FAILED INVALID_ACCOUNT`.
- [ ] AC-10 — Ledger integrity holds after every scenario.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2, AC-8 | integration HTTP | `TestTopUp_Accepted`, `TestTopUp_Errors` (table), `TestTopUp_Frozen` |
| AC-3 | integration HTTP | `TestTopUp_Idempotent`, `TestTopUp_ParallelSameKey` |
| AC-4–AC-6 | integration (fake gateway) | `TestTopUpHandler_Accept`, `TestTopUpHandler_Reject`, `TestTopUpHandler_TemporaryThenAccept`, `TestTopUpHandler_AlreadyProgressed` |
| AC-7 | integration | `TestTopUp_MaxBalanceWithPending`, `TestTopUp_ParallelMaxBalance` |
| AC-9 | manual on compose (recorded in `implementation.md`) | curl walkthrough |
| AC-10 | integration | integrity in cleanup |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Lock during the max-balance check; check order | lock + order above / no lock | Answered | Lock + order above |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Lock the wallet ledger account during the top-up checks; check order 400 → idempotency → 403 → 422 bank account → 422 range → 422 limit | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

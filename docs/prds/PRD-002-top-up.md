# PRD-002 — Top-up (Bank → Wallet)

| Field        | Value                                   |
|--------------|-----------------------------------------|
| Status       | Accepted (2026-11-05, phase-1 roadmap planning) |
| Owner        | Yahya Elaraby                           |
| Created      | 2026-11-03                              |
| Depends on   | PRD-001                                 |
| Related TDRs | TDR-003, TDR-004, TDR-005, TDR-006, TDR-008, TDR-009 |

---

## 1. Summary

A user adds money to their wallet from their linked bank account. The wallet asks the
external bank to **pull** (direct debit) the amount from the user's bank account into the
company's **settlement account**. The bank answers asynchronously by webhook. The wallet
balance increases **only after the bank confirms success**.

## 2. User Stories

- **TU-1** As a user, I request a top-up of an amount; I get an immediate answer that the
  request was accepted with a transaction ID and status `PENDING`.
- **TU-2** As a user, my balance increases by exactly that amount once the bank confirms, and
  the transaction becomes `COMPLETED`.
- **TU-3** As a user, if the bank rejects the debit (e.g. insufficient funds in my bank
  account), the transaction becomes `FAILED` with a readable reason and my balance does not
  change.
- **TU-4** As a user, if I tap "Top up" twice (same idempotency key), I am charged once.
- **TU-5** As a user, if the bank is slow or its webhook is lost, my top-up still completes
  (or fails) correctly without me doing anything.

## 3. Preconditions

1. User is authenticated.
2. Wallet status is `ACTIVE`.
3. User has a linked bank account.
4. `amount_minor` is an integer within top-up limits (PRD-001 §5.6).
5. `current balance + amount_minor` does not exceed the maximum wallet balance.
   *Note:* this is checked at request time; pending top-ups are counted, so the sum of
   balance + all pending/processing top-ups must stay under the maximum.
6. `Idempotency-Key` header is present.

If any check fails, the request is rejected synchronously and **no transaction is created**.

## 4. Flow

```
User ──POST /v1/topups──▶ Wallet API
                           │ one DB transaction:
                           │   - idempotency record
                           │   - transaction (TOPUP, PENDING)
                           │   - outbox event "topup.requested"
                           ◀── 202 {transaction_id, status: PENDING}

Outbox worker ── picks event ──▶ Bank: POST /debits (idempotency key = transaction_id)
                                  ◀── 202 {bank_reference}
                 transaction → PROCESSING, store bank_reference

Bank ──webhook "debit.succeeded"/"debit.failed"──▶ Wallet webhook endpoint
                           │ verify signature, store in inbox (dedupe), 200 OK
Inbox worker ── process ──▶ one DB transaction:
                           success: ledger entry  Dr Bank Settlement / Cr User Wallet
                                    transaction → COMPLETED
                           failure: transaction → FAILED (no ledger entry)
```

## 5. Business Rules

| ID | Rule |
|----|------|
| TU-BR-1 | No ledger entry exists until the bank confirms success. A `PENDING`/`PROCESSING` top-up has **zero** balance effect. |
| TU-BR-2 | On success, exactly one journal entry is posted: **Debit** `Bank Settlement` (asset) / **Credit** `User Wallet` (liability), amount = requested amount. |
| TU-BR-3 | The bank must confirm the **same amount** we requested. If the confirmed amount differs, we **do not** credit; the transaction stays `PROCESSING` and a reconciliation discrepancy is raised for an operator. |
| TU-BR-4 | If the max-balance limit would be exceeded **at completion time** (because of P2P received in between), we still credit — the bank already took the user's money. The limit is a request-time control. |
| TU-BR-5 | Technical failures calling the bank (timeout, 5xx, connection error) are **retried** with exponential backoff using the same idempotency key. Bank business rejections (4xx with reason) make the transaction `FAILED` immediately. |
| TU-BR-6 | A top-up still `PROCESSING` after 10 minutes is checked by the reconciliation job (§7). |
| TU-BR-7 | A top-up still `PENDING` (never accepted by the bank) after max outbox attempts is marked `FAILED` with reason `BANK_UNAVAILABLE` — **only after** reconciliation confirms the bank has no record of it. |
| TU-BR-8 | Frozen wallet: new top-ups are rejected; top-ups already sent to the bank are completed normally when the bank confirms. |

## 6. API Contract (summary)

`POST /v1/topups`

Headers: `Authorization: Bearer <token>`, `Idempotency-Key: <uuid>`

```json
{ "amount_minor": 50000, "currency": "EGP" }
```

`202 Accepted`

```json
{
  "transaction_id": "0193a2c4-…",
  "type": "TOPUP",
  "status": "PENDING",
  "amount_minor": 50000,
  "currency": "EGP",
  "created_at": "2026-11-03T12:00:00Z"
}
```

| Error | HTTP | Code |
|-------|------|------|
| Missing idempotency key | 400 | `IDEMPOTENCY_KEY_REQUIRED` |
| Invalid amount / currency | 400 | `INVALID_AMOUNT` / `UNSUPPORTED_CURRENCY` |
| Below min / above max | 422 | `AMOUNT_OUT_OF_RANGE` |
| Max balance would be exceeded | 422 | `WALLET_LIMIT_EXCEEDED` |
| No linked bank account | 422 | `BANK_ACCOUNT_REQUIRED` |
| Wallet frozen | 403 | `WALLET_FROZEN` |
| Same key, different body | 422 | `IDEMPOTENCY_KEY_REUSED` |
| Same key in progress | 409 | `REQUEST_IN_PROGRESS` |

Status is read via `GET /v1/transactions/{id}`.

## 7. Reconciliation (top-up specific)

The reconciliation job (TDR-009) handles top-ups when:

| Situation | Action |
|-----------|--------|
| `PROCESSING` > 10 min (webhook not received) | Ask bank `GET /v1/operations/{transaction_id}` (TDR-008; aligned 2026-11-05). If final → apply the same handler as the webhook. If still pending → check again later. |
| `PENDING` with outbox event dead (max attempts) | Ask bank. Not found → `FAILED` (`BANK_UNAVAILABLE`). Found → adopt `bank_reference`, continue as `PROCESSING`. |
| Bank says succeeded, our transaction is `FAILED` | **Discrepancy** (critical). Never auto-fix. Operator decides (credit via a new entry or refund at bank). |
| Amount mismatch | **Discrepancy** (critical). |
| Daily statement has a credit to the settlement account we cannot match | **Discrepancy** (unmatched bank movement). |

## 8. Acceptance Criteria

1. Valid request → `202`, transaction `PENDING`, outbox event exists, balance unchanged.
2. Bank success webhook → transaction `COMPLETED`, balance += amount, one balanced journal entry.
3. Bank failure webhook → `FAILED`, balance unchanged, no journal entry.
4. Same webhook delivered 5 times → applied once.
5. Same request with same key sent 2 times in parallel → one transaction, one bank call; the
   other gets `409` or the original response.
6. Webhook never arrives → reconciliation completes the top-up within one job cycle after
   the 10-minute threshold.
7. Bank times out on the first 3 calls → one debit at the bank, transaction completes.
8. Service killed after outbox commit but before the bank call → after restart, the bank is
   called once and the top-up completes.

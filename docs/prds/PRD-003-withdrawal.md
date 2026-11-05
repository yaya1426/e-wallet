# PRD-003 — Withdrawal (Wallet → Bank)

| Field        | Value                                   |
|--------------|-----------------------------------------|
| Status       | Accepted (2026-11-05, phase-1 roadmap planning) |
| Owner        | Yahya Elaraby                           |
| Created      | 2026-11-03                              |
| Depends on   | PRD-001                                 |
| Related TDRs | TDR-003, TDR-004, TDR-005, TDR-006, TDR-008, TDR-009, TDR-010 |

---

## 1. Summary

A user moves money from the wallet to their linked bank account. The amount is **reserved
immediately** (removed from available balance) so it cannot be spent twice, then the wallet
asks the bank to **pay out** from the company settlement account to the user's bank account.
On bank success the reservation is settled; on bank failure the money is **returned** to the
wallet.

## 2. User Stories

- **WD-1** As a user, I request a withdrawal; my available balance drops immediately and I
  see the transaction as `PENDING`.
- **WD-2** As a user, when the bank pays out, the transaction becomes `COMPLETED`.
- **WD-3** As a user, if the bank rejects the payout (e.g. invalid IBAN, closed account), the
  money returns to my wallet and the transaction becomes `FAILED` with a reason.
- **WD-4** As a user, a double click withdraws once.
- **WD-5** As a user, I can never withdraw more than my balance, even if I send many requests
  at the same time.

## 3. Preconditions

1. Authenticated; wallet `ACTIVE`; linked bank account exists.
2. `amount_minor` within withdrawal limits and daily outgoing limit (PRD-001 §5.6).
3. `available balance >= amount_minor` — checked **under a row lock** on the wallet (TDR-010).
4. `Idempotency-Key` header present.

Failure of any check → synchronous rejection; nothing is written and no transaction is created.

## 4. Flow

```
User ──POST /v1/withdrawals──▶ Wallet API
          one DB transaction:
            - lock wallet row (SELECT … FOR UPDATE)
            - check balance & limits
            - idempotency record
            - transaction (WITHDRAWAL, PENDING)
            - ledger entry (reserve): Dr User Wallet / Cr Withdrawals In-Transit
            - update cached wallet balance
            - outbox event "withdrawal.requested"
          ◀── 202 {transaction_id, status: PENDING}

Outbox worker ──▶ Bank: POST /payouts (idempotency key = transaction_id)
                  ◀── 202 {bank_reference}  → transaction PROCESSING

Bank ──webhook "payout.succeeded"/"payout.failed"──▶ inbox ──▶ inbox worker
          success: ledger Dr Withdrawals In-Transit / Cr Bank Settlement → COMPLETED
          failure: ledger Dr Withdrawals In-Transit / Cr User Wallet  → FAILED (refund)
```

## 5. Business Rules

| ID | Rule |
|----|------|
| WD-BR-1 | Funds are **reserved at request time** by a real ledger entry into the `Withdrawals In-Transit` account. There is no separate "hold" concept outside the ledger. |
| WD-BR-2 | On bank success: one entry **Dr** `Withdrawals In-Transit` / **Cr** `Bank Settlement`. Wallet balance does not change again. |
| WD-BR-3 | On bank failure: one reversing entry **Dr** `Withdrawals In-Transit` / **Cr** `User Wallet`. The balance comes back in full. |
| WD-BR-4 | A withdrawal is **never** marked `FAILED` (and refunded) unless the bank confirmed failure **or** reconciliation confirmed the bank has no record of the payout. Refunding on a mere timeout could pay the user twice. |
| WD-BR-5 | Technical bank errors are retried with the same idempotency key (exponential backoff). Business rejections fail immediately (with refund). |
| WD-BR-6 | Bank rejections are **not** auto-retried as a new payout (answer to Q3). The user may submit a new withdrawal. |
| WD-BR-7 | The daily outgoing limit counts withdrawals in `PENDING`, `PROCESSING` and `COMPLETED` for the current Africa/Cairo day. `FAILED` ones do not count. |
| WD-BR-8 | If the wallet is frozen while a withdrawal is in flight, the withdrawal is completed or refunded normally. |
| WD-BR-9 | Confirmed amount from the bank must equal the requested amount; otherwise → discrepancy, no ledger posting, operator action. |

## 6. API Contract (summary)

`POST /v1/withdrawals` — same headers and body shape as top-up.

`202 Accepted` → `{ transaction_id, type: "WITHDRAWAL", status: "PENDING", amount_minor, currency, created_at }`

| Error | HTTP | Code |
|-------|------|------|
| Insufficient balance | 422 | `INSUFFICIENT_FUNDS` |
| Daily limit | 422 | `DAILY_LIMIT_EXCEEDED` |
| Others | — | same as PRD-002 §6 |

## 7. Reconciliation (withdrawal specific)

| Situation | Action |
|-----------|--------|
| `PROCESSING` > 10 min without webhook | Ask bank by idempotency key; apply result with the webhook handler. |
| `PENDING`, outbox event dead | Ask bank. Not found → `FAILED` + refund (`BANK_UNAVAILABLE`). Found → continue. |
| Bank says paid, ours `FAILED` (already refunded) | **Critical discrepancy** — user got money twice. Operator only. |
| Bank says failed, ours `COMPLETED` | **Critical discrepancy**. Operator only. |
| Still unresolved after 24 h | Flag `STUCK` for operator; keep money reserved. |

## 8. Acceptance Criteria

1. Valid request → `202`, available balance reduced, reserve entry posted, outbox event exists.
2. 10 parallel withdrawals of 100 EGP on a 500 EGP balance → exactly 5 accepted, 5 rejected
   `INSUFFICIENT_FUNDS`, final balance 0, never negative.
3. Bank success → `COMPLETED`, settlement entry posted, `Withdrawals In-Transit` back to 0 for it.
4. Bank failure → `FAILED`, balance restored, refund entry posted.
5. Duplicate webhooks → applied once. Late webhook after terminal state → ignored if same
   result; discrepancy if different result.
6. Bank timeout then success on retry → paid once.
7. Lost webhook → reconciliation finishes the withdrawal.

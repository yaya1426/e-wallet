# PRD-004 — Peer-to-Peer Transfer (Wallet → Wallet)

| Field        | Value                                   |
|--------------|-----------------------------------------|
| Status       | Accepted (2026-11-05, phase-1 roadmap planning) |
| Owner        | Yahya Elaraby                           |
| Created      | 2026-11-03                              |
| Depends on   | PRD-001                                 |
| Related TDRs | TDR-003, TDR-004, TDR-005, TDR-010      |

---

## 1. Summary

A user sends money from their wallet to another user's wallet. Both wallets live in our own
ledger, so **no bank is involved**: the transfer is **synchronous** and is either fully
completed or fully rejected inside one database transaction.

## 2. User Stories

- **P2P-1** As a sender, I send an amount to a recipient identified by their **phone number**,
  and I immediately see `COMPLETED` and my new balance.
- **P2P-2** As a recipient, the money appears in my balance immediately and I see the
  transaction in my history as incoming, with the sender's name/phone.
- **P2P-3** As a sender, a double click sends the money once.
- **P2P-4** As a sender, I can add an optional note (max 140 chars).

## 3. Preconditions / Validation

| # | Check | Error |
|---|-------|-------|
| 1 | Authenticated, `Idempotency-Key` present | 401 / 400 |
| 2 | Amount integer, within P2P limits | 400 / 422 `AMOUNT_OUT_OF_RANGE` |
| 3 | Recipient exists | 404 `RECIPIENT_NOT_FOUND` |
| 4 | Recipient ≠ sender | 422 `SELF_TRANSFER_NOT_ALLOWED` |
| 5 | Sender wallet `ACTIVE` | 403 `WALLET_FROZEN` |
| 6 | Sender available balance ≥ amount (under lock) | 422 `INSUFFICIENT_FUNDS` |
| 7 | Sender daily outgoing limit not exceeded | 422 `DAILY_LIMIT_EXCEEDED` |
| 8 | Recipient balance + amount ≤ max wallet balance | 422 `RECIPIENT_LIMIT_EXCEEDED` |

Recipient wallet may be `FROZEN` and still receive (PRD-001 BR-8).

## 4. Flow

```
POST /v1/transfers  {recipient_phone, amount_minor, currency, note}
  one DB transaction:
    - idempotency record
    - lock BOTH wallet ledger-account rows in a fixed order (ascending account ID, TDR-010) — avoids deadlocks
    - validations 5–8
    - transaction (P2P, COMPLETED)
    - ledger entry: Dr Sender Wallet / Cr Recipient Wallet
    - update both cached balances
  commit
◀── 201 {transaction_id, status: COMPLETED, balance_after_minor}
```

## 5. Business Rules

| ID | Rule |
|----|------|
| P2P-BR-1 | All-or-nothing: either both balances change and the entry is posted, or nothing changes. |
| P2P-BR-2 | One transaction record (type `P2P`) is shared by sender and recipient; each sees it with their own direction (`OUT` / `IN`). |
| P2P-BR-3 | A completed P2P transfer **cannot be cancelled or reversed by the user**. Reversals are an operator action via a new compensating entry (future). |
| P2P-BR-4 | No outbox, no bank, no webhook involved. |
| P2P-BR-5 | Recipient is looked up by phone number in E.164 format (`+20…`). The response shows the recipient's masked name to confirm. |

> **Q7 answered (2026-11-05):** yes — `GET /v1/recipients?phone=` returns the recipient's
> masked name only, so the sender can confirm before sending.

## 6. Acceptance Criteria

1. Valid transfer → `201 COMPLETED`, sender −amount, recipient +amount, one balanced entry.
2. A→B and B→A sent concurrently many times → no deadlock errors surface to users, final
   balances are correct.
3. 20 parallel transfers draining a wallet → never negative.
4. Same idempotency key twice → one transfer; second response equals the first.
5. Ledger integrity check passes after the scenarios.

# TDR-004 — Double-Entry Ledger as System of Record

| Field      | Value |
|------------|-------|
| Status     | Proposed |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-001 §5.4, PRD-002, PRD-003, PRD-004, TDR-010 |

## Context
We need to prove at any time where every piaster is. A single `balance` column updated in
place cannot explain itself and cannot be audited. Double-entry bookkeeping guarantees that
money is only *moved*, never created or destroyed: every entry's debits equal its credits.

## Decision

### 1. Two separate concepts
| Concept | Table(s) | Purpose | Mutable? |
|---------|----------|---------|----------|
| **Transaction** (business) | `transactions` | What the user asked for: type, status, amount, counterparty, bank reference, failure reason. | Status fields only, through a state machine. |
| **Ledger** (accounting) | `ledger_accounts`, `journal_entries`, `postings` | The system of record of money. | **Append-only.** No UPDATE / DELETE (enforced by DB trigger and DB role grants). |

One transaction can have **0..n journal entries** (e.g. withdrawal = reserve entry + settle
entry, or reserve entry + refund entry).

### 2. Chart of accounts
| Account | Code | Type | Normal side | One per |
|---------|------|------|-------------|---------|
| User wallet | `USER_WALLET:{wallet_id}` | Liability (we owe the user) | Credit | user |
| Bank settlement | `BANK_SETTLEMENT` | Asset (money in company bank account) | Debit | system |
| Withdrawals in transit | `WITHDRAWALS_IN_TRANSIT` | Liability (reserved, not yet paid out) | Credit | system |
| Suspense | `SUSPENSE` | Liability/clearing for operator corrections | — | system |

System accounts are created by a migration (seed). A future `FEE_REVENUE` account can be
added without changing anything existing.

### 3. Postings per flow
| Event | Debit | Credit |
|-------|-------|--------|
| Top-up confirmed | `BANK_SETTLEMENT` | `USER_WALLET` |
| Withdrawal requested (reserve) | `USER_WALLET` | `WITHDRAWALS_IN_TRANSIT` |
| Withdrawal paid by bank | `WITHDRAWALS_IN_TRANSIT` | `BANK_SETTLEMENT` |
| Withdrawal failed (refund) | `WITHDRAWALS_IN_TRANSIT` | `USER_WALLET` |
| P2P transfer | `USER_WALLET(sender)` | `USER_WALLET(recipient)` |

### 4. Storage rules
- `postings(direction ∈ {DEBIT, CREDIT}, amount BIGINT > 0)` — amounts are always positive;
  the direction gives the sign. Signed balance of an account =
  Σcredits − Σdebits for credit-normal accounts, Σdebits − Σcredits for debit-normal.
- Each journal entry is balanced: enforced in Go **and** by a `DEFERRABLE INITIALLY DEFERRED`
  constraint trigger that checks Σdebits = Σcredits per entry at commit.
- Each journal entry references `transaction_id` and has an `entry_type`
  (`TOPUP_SETTLE`, `WITHDRAWAL_RESERVE`, `WITHDRAWAL_SETTLE`, `WITHDRAWAL_REFUND`, `P2P`, `ADJUSTMENT`).
- `UNIQUE (transaction_id, entry_type)` — the same business step can never be posted twice.
  This is the ledger-level idempotency guard behind every webhook/retry path.

### 5. Cached balance
- `ledger_accounts.balance BIGINT` is a **projection** updated in the **same DB transaction**
  as the postings, after locking the account row (TDR-010). Reads (show balance, limit checks)
  use it — no SUM over history on the hot path.
- `CHECK (balance >= 0)` on user-wallet accounts is the final guard against overdraft.
- `ledger_accounts.version BIGINT` incremented on each change (debugging and optimistic
  reads).
- Each posting stores `balance_after` for the account → statement lines without recomputation.

### 6. Integrity checks (run by the reconciliation job and in tests)
1. Σ all debits = Σ all credits across the whole ledger.
2. For every account: cached `balance` = balance computed from postings.
3. `WITHDRAWALS_IN_TRANSIT` balance = Σ amounts of withdrawals in `PENDING`/`PROCESSING`.
4. `BANK_SETTLEMENT` balance = company bank account balance from the bank (daily, TDR-009).

Any failure is written to `reconciliation_discrepancies` and alerts.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Single balance column only | Not auditable; can't explain or reconcile. |
| Balance computed only from SUM(postings) | Correct but slow on hot path and hard to lock for overdraft checks. |
| Event sourcing with separate read models | More moving parts; the ledger already is an append-only log. |
| External ledger (TigerBeetle, Formance) | Very good fit later; adds a second datastore now and breaks single-commit atomicity with outbox. |

## Consequences
- + Every balance is explainable and verifiable; corrections are visible entries.
- − More rows per movement; posting code must be centralized in the ledger module
  (`ledger.Post(ctx, tx, Entry)`) — no other module writes postings.

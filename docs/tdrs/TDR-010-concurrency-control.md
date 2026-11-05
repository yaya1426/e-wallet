# TDR-010 — Concurrency Control for Balances

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-003, PRD-004, TDR-002, TDR-004 |

## Context
Parallel requests on the same wallet (double clicks with different keys, scripts, two devices)
must never overspend. P2P transfers lock two wallets and can deadlock if A→B and B→A run
together.

## Decision
1. **Pessimistic row locks** at isolation level `READ COMMITTED`:
   every operation that changes a wallet balance does
   `SELECT … FROM ledger_accounts WHERE id = ANY($ids) ORDER BY id FOR UPDATE`
   **before** checking the balance, inside the same DB transaction that posts the entry.
2. **Lock order:** always ascending account ID (UUIDv7 byte order). Applies to P2P and to any
   entry touching several accounts. Removes A↔B deadlocks.
3. **System accounts are not locked on the hot path.** `BANK_SETTLEMENT` and
   `WITHDRAWALS_IN_TRANSIT` are touched by every bank flow; locking them would serialize all
   users. Their cached balance is updated with an atomic `UPDATE … SET balance = balance + $d`
   (row lock held only for the statement), and they are allowed to be any sign by design. They
   are verified by the integrity job (TDR-004 §6). *Alternative if contention appears:* skip
   the cached balance for system accounts and compute it on demand.
4. **DB guard:** `CHECK (balance >= 0)` on user-wallet accounts — if the code ever forgets a
   check, the commit fails instead of overdrawing.
5. **Timeouts:** `SET LOCAL lock_timeout = '2s'` and `statement_timeout = '5s'` for money
   transactions; on lock timeout or deadlock (`40P01`) / serialization error (`40001`) the
   use case **retries the whole DB transaction** up to 3 times with small jitter, then returns
   `503 TRY_AGAIN`. (Decided 2026-11-05: the numbers are config — `DB_LOCK_TIMEOUT=2s`,
   `DB_STATEMENT_TIMEOUT=5s`, `DB_TX_MAX_RETRIES=3` — and the wait between attempts is a random
   10–50 ms. Lock timeout = SQLSTATE `55P03`. Implemented by `database.DB.InTx`, T002.)
6. **Short transactions:** no network call (bank, etc.) ever happens while a DB transaction is
   open. Bank calls happen in workers, outside DB transactions; their results are applied in a
   new short transaction.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Optimistic locking (version check + retry) | Many retries under contention on hot wallets; pessimistic is simpler to reason about for money. |
| `SERIALIZABLE` everywhere | Correct, but more aborts and retries; explicit locks give the same safety here with fewer surprises. |
| Application-level mutex | Does not work across instances. |

## Consequences
- + Overspend is impossible; deadlocks avoided by ordering.
- − A very hot wallet serializes its own operations (acceptable: per-user volume is low).

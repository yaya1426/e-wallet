# T016 — Discrepancy module + `ApplyBankOutcome` for debits (top-up completion)

| Field        | Value |
|--------------|-------|
| ID           | T016 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T016-discrepancy-apply-bank-outcome` |
| Depends on   | T006, T013 (types), T015 |
| PRDs         | PRD-002 TU-BR-1..4, TU-BR-8, §7 (discrepancy rows), §8 AC-2..4; PRD-001 BR-8, BR-10, BR-15 |
| TDRs         | TDR-016, TDR-007 §3, TDR-009 §3, TDR-004 §3, TDR-005 L4 |
| Docs changed | — |

## What
When the bank reports the result of a **debit**, the top-up is completed (credit the wallet) or
failed, exactly once. Anything that does not add up is recorded as a discrepancy for an operator.

Deliverables:
- `internal/discrepancy`: `reconciliation_discrepancies` table, `Record(ctx, tx, …)`, `List(ctx, filter)`.
- `internal/payment`: `ApplyBankOutcome(ctx, tx, txnID, bank.Outcome)` (debit part) and
  `HandleBankEvent` (implements `webhook.EventHandler`), wired in `cmd/wallet`.
- The `UNKNOWN_BANK_EVENT` discrepancy when an inbox event goes `DEAD` because no transaction matches.

## Why
TDR-016 (module), TDR-007 §3 (find by bank reference or idempotency key, lock, the shared
`ApplyBankOutcome` rules), TDR-009 §3 (kinds, never auto-fix contradictions), PRD-002 TU-BR-1..4
(no ledger until confirmed, one `TOPUP_SETTLE` entry, amount must match, credit even above the
max balance at completion), TU-BR-8 / BR-8 (frozen wallet still credited), BR-15 (terminal
states). Decided 2026-11-05: the failure code/message shown to the user = the bank's.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000009_discrepancies.up/down.sql` | Table |
| `internal/discrepancy/model.go`, `service.go`, `queries.sql` | `Kind`, `Severity`, `Discrepancy`, `Record`, `List` |
| `internal/bank/gateway.go` | Add `Outcome{Kind, Status (SUCCEEDED/FAILED), BankReference, Amount, FailureCode, FailureMessage}` (shared by webhook and phase-2 recon; built from a `webhook.BankEvent` or a `bank.Operation`) |
| `internal/payment/outcome.go` | `ApplyBankOutcome`, `HandleBankEvent` |
| `internal/payment/service.go` | `Service` constructor (deps: wallet, transaction, ledger, discrepancy, clock) |
| `internal/wallet/service.go` | `Get(ctx, q, walletID)` if T004 did not add it |
| `internal/webhook/service.go` | On `DEAD` caused by `ErrUnknownTransaction` → `discrepancy.Record(UNKNOWN_BANK_EVENT)` in its own `InTx` |
| `cmd/wallet/app.go` | `webhook.SetHandler(paymentService)` → the inbox runner starts |
| `.golangci.yml` | depguard: `discrepancy` imports only platform; add it to the platform deny list; `payment` imports `wallet`, `transaction`, `ledger`, `outbox`, `bank`, `discrepancy`, `idempotency`, `identity` + platform; `webhook` may import `discrepancy` |

### Data (migrations)
```sql
reconciliation_discrepancies(id uuid PK,
  kind text NOT NULL CHECK (kind IN ('STATUS_MISMATCH','AMOUNT_MISMATCH','UNMATCHED_BANK_MOVEMENT',
     'UNMATCHED_LEDGER_MOVEMENT','STUCK_TRANSACTION','UNKNOWN_BANK_EVENT','LEDGER_INTEGRITY')),
  severity text NOT NULL CHECK (severity IN ('CRITICAL','WARNING')),
  transaction_id uuid NULL REFERENCES transactions(id),
  details jsonb NOT NULL,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','RESOLVED')),
  created_at timestamptz NOT NULL, resolved_at timestamptz NULL, resolution_note text NULL)
UNIQUE INDEX (transaction_id, kind) WHERE status = 'OPEN' AND transaction_id IS NOT NULL
INDEX (status, created_at)
```
`Record` = `INSERT … ON CONFLICT DO NOTHING`, so the same open problem for a transaction is recorded once.

### API
None (the operator API is phase 2).

### Behaviour and design notes
**`HandleBankEvent(ctx, tx, ev)`** (debit events only in this task; `payout.*` → returns
`ErrUnsupportedEvent`, which the inbox retries until T019 adds payouts):
1. Find the transaction: `transaction.FindByBankRef(ev.BankReference)`, else `Get(ev.IdempotencyKey)`
   (our transaction ID, TDR-007 §3.1). Neither → `webhook.ErrUnknownTransaction`.
2. `transaction.GetForUpdate(tx, id)`.
3. `ApplyBankOutcome(ctx, tx, id, outcome)`.

**`ApplyBankOutcome`** for a `TOPUP` (txn already locked by the caller):
| Current state / check | Action |
|-----------------------|--------|
| Outcome kind ≠ the transaction type (debit result for a non-TOPUP) | `STATUS_MISMATCH` (CRITICAL, details: expected/actual kind), no change |
| Terminal and the outcome agrees (COMPLETED + SUCCEEDED, FAILED + FAILED) | no-op |
| Terminal and the outcome contradicts | `STATUS_MISMATCH` (CRITICAL), no change (BR-15) |
| Outcome amount ≠ transaction amount | `AMOUNT_MISMATCH` (CRITICAL), no posting; status stays as is (`PROCESSING`, or `PENDING` → see next row first) (TU-BR-3) |
| `PENDING` (webhook arrived before the outbox worker stored the reference) | first `Transition(PENDING→PROCESSING, bank_reference = outcome.BankReference)`, then continue as `PROCESSING` |
| `PROCESSING` + `SUCCEEDED` | `ledger.Post(TOPUP_SETTLE: Dr BANK_SETTLEMENT / Cr USER_WALLET(destination))`, then `Transition(PROCESSING→COMPLETED)` |
| `PROCESSING` + `FAILED` | `Transition(PROCESSING→FAILED, failure_code/message = the bank's)`, no ledger entry |
- Stored `bank_reference` ≠ outcome's (both non-empty) → `STATUS_MISMATCH` (CRITICAL), no change.
- The wallet is credited even if it is `FROZEN` (BR-8) and even if the balance goes above
  `LIMIT_MAX_BALANCE` (TU-BR-4). The credit does not need `LockAccounts` (no balance check). The
  atomic `UPDATE` in `Post` is enough.
- `ledger.ErrDuplicateEntry` cannot happen in a correct run (the status check comes first). If it
  does, it is returned as an error (the event is retried and the logs show it), never ignored silently.
- A discrepancy commits in the same tx as everything else. Discrepancy results return `nil`
  (the event is `DONE`; the operator takes over).
- The order of the amount check vs the PENDING step: the kind, terminal and amount checks run
  **before** any transition.

### Out of scope
Payout outcomes (T019), reconciliation callers (phase 2), the operator API (phase 2), auto-fix.

## Acceptance Criteria
- [ ] AC-1 — Given a `PROCESSING` top-up of 50 000, when a `debit.succeeded` with the same amount is processed, then `COMPLETED` with `completed_at`, wallet +50 000, `BANK_SETTLEMENT` +50 000, one `TOPUP_SETTLE` entry (PRD-002 AC-2).
- [ ] AC-2 — `debit.failed` (`INSUFFICIENT_FUNDS`) → `FAILED` with that code and message, no journal entry, balance unchanged (AC-3).
- [ ] AC-3 — The same event 5 times (different `event_id`s, so the inbox does not dedupe) → applied once; later ones are no-ops (AC-4).
- [ ] AC-4 — `debit.failed` after `COMPLETED` (or `succeeded` after `FAILED`) → one `STATUS_MISMATCH` CRITICAL row, no money/status change; repeating it does not add a second open row.
- [ ] AC-5 — Amount 49 999 instead of 50 000 → `AMOUNT_MISMATCH`, no posting, still `PROCESSING`.
- [ ] AC-6 — A `PENDING` top-up (no reference yet) receiving `debit.succeeded` matched by idempotency key → `PROCESSING` with the event's reference, then `COMPLETED`.
- [ ] AC-7 — Frozen wallet, or a balance that would exceed the max → still credited.
- [ ] AC-8 — An event for an unknown transaction → retried; at `DEAD` one `UNKNOWN_BANK_EVENT` (WARNING, no transaction_id, details = event id/type/reference) is recorded.
- [ ] AC-9 — A debit event for a `WITHDRAWAL` transaction → `STATUS_MISMATCH`, no change.
- [ ] AC-10 — `discrepancy.List` returns rows filtered by status, newest first.
- [ ] AC-11 — Ledger integrity holds after every scenario.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-7, AC-9 | integration | `TestApplyBankOutcome_TopUp` (table over state × outcome), setup through `transaction.Create` |
| AC-3 | integration | `TestHandleBankEvent_RepeatedOutcomes` |
| AC-6 | integration | `TestHandleBankEvent_BeforeBankReference` |
| AC-8 | integration | `TestInbox_DeadUnknownRecordsDiscrepancy` |
| AC-10 | integration | `TestDiscrepancy_RecordDedupAndList` |
| AC-11 | integration | integrity in cleanup |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Failure code shown to the user; amount mismatch handling | bank's code as-is, stay PROCESSING / other | Answered | Bank's code as-is; stays PROCESSING + AMOUNT_MISMATCH |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | `failure_code/message` = bank's; amount mismatch → CRITICAL `AMOUNT_MISMATCH`, stays PROCESSING | Yahya | this task |
| 2026-11-05 | Severity values `CRITICAL` (status/amount mismatch) / `WARNING` (unknown event); one open discrepancy per (transaction, kind); `PENDING` + final outcome → PROCESSING first | planning (from TDR-007/009 wording) | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

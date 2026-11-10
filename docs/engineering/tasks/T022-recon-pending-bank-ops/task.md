# T022 — Reconciliation job `pending_bank_ops` (lost webhooks)

| Field        | Value |
|--------------|-------|
| ID           | T022 |
| Status       | Ready |
| Created      | 2026-11-10 |
| Branch       | `task/T022-recon-pending-bank-ops` |
| Depends on   | T021 (scheduler), T013 (`bank.GetOperation`), T016/T019 (`ApplyBankOutcome`) |
| PRDs         | PRD-002 TU-BR-6, §7 row 1; PRD-003 §7 row 1; PRD-001 BR-15 |
| TDRs         | TDR-009 §2–3, TDR-007 §3, TDR-008 §1, TDR-010 §6, TDR-016 |
| Docs changed | — |

## What
A top-up or withdrawal stays `PROCESSING` forever if the bank's webhook never arrives. This job
asks the bank for the status of such transactions and applies a final answer through the same
`payment.ApplyBankOutcome` the webhook uses, so lost webhooks heal themselves in ~11 minutes.

Deliverables:
- `internal/reconciliation/pending_bank_ops.go`: the job, registered with the T021 scheduler.
- `transaction`: `ListForReconciliation`, `MarkReconciled`; migration `000011` index.
- Config `RECON_PENDING_INTERVAL`, `RECON_PENDING_MIN_AGE`, `RECON_PENDING_RECHECK`, `RECON_PENDING_BATCH`.
- `wallet reconcile --job=pending_bank_ops` works (needs `BANK_BASE_URL`).

## Why
TDR-009 §2 (`pending_bank_ops`: every 1 min, `PROCESSING` > 10 min, not checked in the last 5 min,
batch 100, `GetOperation(transaction_id)`, apply via `ApplyBankOutcome`, still pending → try later),
§3 (auto-fix only a clear final answer for a non-terminal transaction; contradictions are recorded,
never fixed). PRD-002 TU-BR-6 and PRD-002/003 §7 row 1.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/reconciliation/pending_bank_ops.go` | `PendingBankOps(deps, cfg) reconciliation.Job` |
| `internal/transaction/queries.sql`, `service.go` | `ListForReconciliation`, `MarkReconciled` |
| `migrations/000011_transactions_recon_idx.up/down.sql` | Index below |
| `internal/platform/config` | The four `RECON_PENDING_*` settings |
| `cmd/wallet/app.go` | Register the job in `serve`; the `reconcile` command builds the bank client too (validates `BANK_BASE_URL`) |

### Data (migrations)
```sql
CREATE INDEX transactions_processing_created_idx ON transactions (created_at)
    WHERE status = 'PROCESSING';
```

### Configuration
| Variable | Default | Rule |
|----------|---------|------|
| `RECON_PENDING_INTERVAL` | `1m` | > 0; the job timeout is `Interval − 5 s`, at least 1 s |
| `RECON_PENDING_MIN_AGE` | `10m` | > 0 |
| `RECON_PENDING_RECHECK` | `5m` | > 0 |
| `RECON_PENDING_BATCH` | `100` | 1..1000 |

### API
None (HTTP).

### Behaviour and design notes
**Selection** (`transaction.ListForReconciliation(ctx, q, createdBefore, checkedBefore, limit)`):
`status = 'PROCESSING' AND type IN ('TOPUP','WITHDRAWAL') AND created_at < now − MIN_AGE AND
(last_reconciled_at IS NULL OR last_reconciled_at < now − RECHECK)`, oldest `created_at` first,
`LIMIT BATCH`. Age is measured from `created_at` (decided 2026-11-10).
`transaction.MarkReconciled(ctx, q, id, at)` sets `last_reconciled_at = at` only (not `updated_at`
and not the status: it is bookkeeping, not a business change).

**Per transaction, one at a time** (no DB transaction is open during the bank call, TDR-010 §6):
1. `op, err := bank.GetOperation(ctx, txn.ID)` (the idempotency key is the transaction ID).
2. `err` is `bank.ErrNotFound` → `InTx`: `GetForUpdate`; if still `PROCESSING`, record a CRITICAL
   `STATUS_MISMATCH` (details: reason "the bank has no operation for a PROCESSING transaction",
   type, amount, stored reference) and `MarkReconciled`. No status or money change (decided
   2026-11-10). One open row per transaction (the discrepancy module dedupes).
3. Any other error (`ErrTemporary`, timeout, ctx) → skip this transaction: `last_reconciled_at` stays
   as is so the next tick retries it; remember the error (decided 2026-11-10).
4. `op.Status == PENDING` → `MarkReconciled` (checked again after `RECHECK`).
5. Final → `InTx`: `payment.ApplyBankOutcome(ctx, tx, txn.ID, op.Outcome())` then `MarkReconciled`
   in the same transaction. `ApplyBankOutcome` already locks the row and handles: already applied by
   a webhook (no-op), kind/reference/amount mismatches (discrepancy), PENDING step, settle/refund.
   An error from it rolls back and counts as an error (step 3 behaviour).
- **Counts:** `Checked` = transactions handled in steps 2–5; `Fixed` = transactions whose status
  became `COMPLETED` or `FAILED` in this run (re-read the status inside the step-5 transaction
  after `ApplyBankOutcome`).
- **Run result:** `FAILED` (with the joined errors, at most the first 5) only when the batch was
  non-empty and **every** bank call failed (bank down); otherwise `SUCCESS`, even if some were
  skipped (their errors are logged with `transaction_id`). An empty batch is `SUCCESS` with 0/0.
- When ctx ends mid-batch (timeout or shutdown) the job stops and returns what it did; the rest is
  picked up next tick.
- Logs per transaction: `transaction_id`, `type`, `bank_status`, result (`applied`, `still_pending`,
  `not_found`, `error`).
- **Imports:** `reconciliation` → `payment`, `transaction`, `bank`, `discrepancy`, platform (allowed,
  architecture §3.2).

### Out of scope
`dead_outbox` and `stuck_alert` (T023), PENDING transactions (T023), simulator fault scenarios that
drop webhooks (P2-1), resilience e2e (P2-6).

## Acceptance Criteria
- [ ] AC-1 — Given a `PROCESSING` top-up older than 10 min whose webhook was lost and the bank says `SUCCEEDED`, when the job runs, then it is `COMPLETED`, the wallet is credited once (`TOPUP_SETTLE`), `last_reconciled_at` is set, and the run reports checked 1 / fixed 1.
- [ ] AC-2 — The same for a withdrawal: bank `SUCCEEDED` → `COMPLETED` + `WITHDRAWAL_SETTLE`; bank `FAILED` → `FAILED` with the bank's code + `WITHDRAWAL_REFUND`; the in-transit invariant holds.
- [ ] AC-3 — Bank still `PENDING` → only `last_reconciled_at` changes; a run within `RECHECK` does not ask again; after `RECHECK` it does.
- [ ] AC-4 — A `PROCESSING` transaction younger than `MIN_AGE`, a `PENDING` or a terminal one, or a P2P → not selected; at most `BATCH` per run, oldest first.
- [ ] AC-5 — Bank `NOT_FOUND` → one CRITICAL `STATUS_MISMATCH`, status and money unchanged; repeated runs do not add a second open row.
- [ ] AC-6 — One transaction's bank call fails temporarily, another succeeds → the second is applied, the first is untouched (`last_reconciled_at` unchanged) and retried next run; the run is `SUCCESS`. All calls fail → run `FAILED` with the error text.
- [ ] AC-7 — Bank amount ≠ ours → `AMOUNT_MISMATCH` (via `ApplyBankOutcome`), stays `PROCESSING`.
- [ ] AC-8 — The webhook already applied the outcome (or arrives at the same time) → no double posting; the job's apply is a no-op.
- [ ] AC-9 — Config defaults and validation for the four variables; `wallet reconcile --job=pending_bank_ops` runs it once against the configured bank.
- [ ] AC-10 — Ledger integrity and the in-transit invariant hold after every scenario.
- [ ] AC-11 — Manual check on compose (recorded in `implementation.md`): with banksim's webhook URL pointed at a wrong path and `RECON_PENDING_MIN_AGE=10s`, a top-up stays `PROCESSING`, then the job completes it.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-8, AC-10 | integration (testcontainers + `banktest` fake + fake clock) | `TestPendingBankOps_*` (table over bank answer × transaction type) |
| AC-4 | integration | `TestListForReconciliation` (transaction module) |
| AC-9 | unit + integration | `TestConfig_ReconPending`, `TestReconcile_CLI_PendingBankOps` |
| AC-11 | manual on compose | recorded walkthrough |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Bank says NOT_FOUND for a PROCESSING transaction | discrepancy, no change / fail it / ignore | Answered | CRITICAL `STATUS_MISMATCH`, no change |
| 2 | Age basis for "PROCESSING > 10 min" | created_at / updated_at / new column | Answered | `created_at` |
| 3 | Temporary bank error for one transaction | skip and continue / stop the run | Answered | Skip, continue; run FAILED only if all calls failed |
| 4 | Thresholds configurable | env vars with TDR defaults / hard-coded | Answered | Env vars, TDR-009 defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | NOT_FOUND for a PROCESSING transaction → CRITICAL `STATUS_MISMATCH`, no status/money change | Yahya | this task |
| 2026-11-10 | Age measured from `created_at`; `MarkReconciled` does not touch `updated_at` | Yahya / planning | this task |
| 2026-11-10 | Temporary bank error → skip that transaction, keep going; run FAILED only if every call failed | Yahya | this task |
| 2026-11-10 | `RECON_PENDING_*` env vars with TDR-009 values as defaults | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |

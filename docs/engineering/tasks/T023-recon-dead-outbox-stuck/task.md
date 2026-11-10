# T023 — Reconciliation jobs `dead_outbox` and `stuck_alert`

| Field        | Value |
|--------------|-------|
| ID           | T023 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T023-recon-dead-outbox-stuck` |
| Depends on   | T021 (scheduler), T022 (job pattern), T012 (outbox), T017/T018 (request events), T016/T019 (`ApplyBankOutcome`) |
| PRDs         | PRD-002 TU-BR-7, §7 row 2; PRD-003 WD-BR-4, §7 rows 2 and 5 |
| TDRs         | TDR-009 §2–3, TDR-006 §3, TDR-010 §6, TDR-016 |
| Docs changed | — |

## What
Two more reconciliation jobs on the T021 scheduler:
- **`dead_outbox`** resolves top-ups and withdrawals left `PENDING` because their bank request
  (`topup.requested` / `withdrawal.requested`) went `DEAD` in the outbox. It asks the bank:
  no record → `FAILED` with `BANK_UNAVAILABLE` (a withdrawal is refunded); a record → carry on
  from what the bank knows. Each handled dead event is marked resolved in the outbox.
- **`stuck_alert`** flags top-ups and withdrawals still not final after 24 h as a
  `STUCK_TRANSACTION` discrepancy and exposes the count as a gauge. Money stays as it is.

## Why
TDR-009 §2 (`dead_outbox` every 5 min: not found → `FAILED` `BANK_UNAVAILABLE` + refund; found →
store the reference, `PROCESSING`, continue. `stuck_alert` every 15 min: non-terminal bank
transactions older than 24 h → `STUCK_TRANSACTION`, metric + alert, money untouched); PRD-002
TU-BR-7 ("only after reconciliation confirms the bank has no record"); PRD-003 WD-BR-4 (refund only
when the bank confirms) and §7. Decided 2026-11-10: the questions below.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/outbox/queries.sql`, `service.go` | `ListDead`, `Resolve` (below) |
| `internal/payment/unreachable.go` | `FailUnreachable(ctx, tx, txnID)` |
| `internal/transaction/queries.sql`, `service.go` | `ListStuck`, `CountStuck` |
| `internal/reconciliation/dead_outbox.go`, `stuck_alert.go` | The jobs |
| `internal/platform/config` | `RECON_DEAD_INTERVAL` (5m), `RECON_DEAD_BATCH` (100, 1..1000), `RECON_STUCK_INTERVAL` (15m), `RECON_STUCK_AGE` (24h) — all > 0 |
| `internal/platform/metrics` | Gauge `recon_stuck_transactions{type}` (`TOPUP`, `WITHDRAWAL`) |
| `cmd/wallet/app.go` | Register both jobs (`reconJobs`); the jobs' deps grow (outbox) |
| `configs/.env.example` | The four variables |

### Data (migrations)
None. (`outbox_events` already allows `DONE`; `transactions_open_status_idx` covers the stuck query.)

### API
None (HTTP). `wallet reconcile --job=dead_outbox|stuck_alert` work through T021.

### Behaviour and design notes
**Outbox API** (decided 2026-11-10; the outbox keeps owning its table):
```text
type DeadEvent struct { ID, AggregateID uuid.UUID; Type, LastError string; Attempts int; CreatedAt time.Time }
func (s *Service) ListDead(ctx, q Querier, types []string, limit int) ([]DeadEvent, error) // status DEAD, oldest first
func (s *Service) Resolve(ctx, q Querier, eventID uuid.UUID, note string) error
// DEAD → DONE, processed_at = now, last_error = "resolved by reconciliation: " + note;
// ErrNotDead when the event is not DEAD (resolved by another run): the caller's tx is rolled back.
```

**`payment.FailUnreachable(ctx, tx, txnID)`**: locks the transaction; if it is still `PENDING`:
top-up → `PENDING → FAILED`; withdrawal → `failAndRefund` (`FAILED` + `WITHDRAWAL_REFUND`). Code
`BANK_UNAVAILABLE`, message `The bank could not be reached. No money was moved.` (decided
2026-11-10, same text for both). Not `PENDING` → `transaction.ErrStatusConflict` (nothing changes).

**`dead_outbox`** (interval `RECON_DEAD_INTERVAL`, timeout = interval − 5 s, at least 1 s, at most the
interval, like T022): `ListDead([topup.requested, withdrawal.requested], RECON_DEAD_BATCH)`; per
event, one at a time, no DB transaction open during the bank call:
1. Load the transaction (`AggregateID`). Missing → `Resolve(note "transaction not found")` and log an
   error. Not `PENDING` any more (a webhook or `pending_bank_ops` moved it) → `Resolve(note "already
   <STATUS>")`; checked, not fixed.
2. `bank.GetOperation(txn.ID)`:
   - **`ErrNotFound`** → `InTx`: `FailUnreachable` + `Resolve(note "bank has no record; BANK_UNAVAILABLE")`.
     Fixed. (`ErrStatusConflict` → the transaction moved meanwhile: `Resolve(note "already …")` instead.)
   - **other error** → skip; the event stays `DEAD` and is tried next run. The run is `FAILED` only when
     every bank call of a non-empty batch failed (same rule as T022).
   - **`PENDING` at the bank** → `InTx`: if still `PENDING`, `Transition(PENDING → PROCESSING, bank_reference
     = op.BankReference)`; `Resolve(note "bank has it; PROCESSING")`. `pending_bank_ops` takes over.
   - **final** → `InTx`: `payment.ApplyBankOutcome(op.Outcome())` (does the PENDING → PROCESSING step,
     settle/refund, discrepancies) + `Resolve(note "bank answered <STATUS>")`. Fixed when the transaction
     became final.
   Each `InTx` contains the business change **and** the `Resolve`, so a crash never leaves one without
   the other. `MarkReconciled` is also set on the transaction in the same tx.

**`stuck_alert`** (interval `RECON_STUCK_INTERVAL`, timeout = min(interval − 5 s ≥ 1 s, interval)):
1. `transaction.ListStuck(ctx, q, createdBefore = now − RECON_STUCK_AGE, limit 1000)`: `TOPUP`/`WITHDRAWAL`
   in `PENDING` or `PROCESSING`, oldest first.
2. For each, `discrepancy.Record` `STUCK_TRANSACTION` in its own `InTx`; severity **CRITICAL for a
   withdrawal, WARNING for a top-up** (decided 2026-11-10); details: `type`, `status`, `amount_minor`,
   `bank_reference`, `created_at`, `age_hours`. The open-row dedupe (TDR-016) keeps one row per
   transaction. No money or status change; withdrawals stay reserved (PRD-003 §7).
3. `transaction.CountStuck(ctx, q, createdBefore)` → per type; set the gauge
   `recon_stuck_transactions{type}` (decided 2026-11-10) — also to 0 when none, so the alert clears.
4. Log at WARN when the count is > 0. Result: checked = listed, fixed = 0.

**Imports:** `reconciliation` → `outbox`, `payment`, `transaction`, `bank`, `discrepancy` (allowed).

### Out of scope
The operator API (P2-5), resolving discrepancies, simulator fault scenarios (P2-1), resilience e2e
(P2-6), an outbox archive janitor (P2-1).

## Acceptance Criteria
- [x] AC-1 — Given a `PENDING` top-up whose `topup.requested` is `DEAD` and the bank has no record, when `dead_outbox` runs, then it is `FAILED` `BANK_UNAVAILABLE` with the agreed message, no journal entry, and the event is `DONE` with a "resolved by reconciliation" note.
- [x] AC-2 — Same for a withdrawal: `FAILED` `BANK_UNAVAILABLE` + one `WITHDRAWAL_REFUND`, balance restored, in-transit invariant holds.
- [x] AC-3 — The bank has the operation `PENDING` → the transaction is `PROCESSING` with the bank's reference and the event resolved; a later `pending_bank_ops` run completes it.
- [x] AC-4 — The bank has a final answer → applied through `ApplyBankOutcome` (top-up credited / withdrawal settled or refunded), event resolved.
- [x] AC-5 — The transaction already moved on (webhook applied) → nothing changes, event resolved with "already …".
- [x] AC-6 — Temporary bank error → the event stays `DEAD` and is retried next run; others in the batch are handled; all failing → run `FAILED`.
- [x] AC-7 — A resolved event is never picked again; two concurrent runs (CLI + scheduler) cannot both change the same transaction (advisory lock + `Resolve` guard).
- [x] AC-8 — Only `topup.requested` / `withdrawal.requested` dead events are handled; other types are left alone.
- [x] AC-9 — `stuck_alert`: top-ups and withdrawals `PENDING`/`PROCESSING` older than 24 h → one open `STUCK_TRANSACTION` each (CRITICAL withdrawal, WARNING top-up); younger or final ones are not flagged; repeated runs add no second open row; money and status unchanged; the gauge equals the counts per type and returns to 0 when they are resolved.
- [x] AC-10 — Config defaults and validation; both jobs registered (`reconcile --job=dead_outbox` / `stuck_alert` run once).
- [x] AC-11 — Ledger integrity and the in-transit invariant hold after every scenario.
- [x] AC-12 — Manual check on compose (recorded in `implementation.md`): with `BANK_BASE_URL` broken for the outbox and short thresholds, a top-up's event goes `DEAD`, then `dead_outbox` fails it with `BANK_UNAVAILABLE` (the simulator never received it).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-8, AC-11 | integration (testcontainers, `banktest`, fake clock; dead events made by running the outbox with a failing handler or by marking them DEAD through the outbox store) | `TestDeadOutbox_*` |
| AC-9, AC-11 | integration | `TestStuckAlert_*` |
| — | integration | `TestOutbox_ListDeadAndResolve`, `TestFailUnreachable` |
| AC-10 | unit + integration | `TestConfig_ReconDeadStuck`, `TestReconcile_CLI_*` |
| AC-12 | manual on compose | recorded walkthrough |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | How reconciliation reads dead outbox events | outbox API ListDead + Resolve / transactions only | Answered | Outbox API |
| 2 | Message for `BANK_UNAVAILABLE` | one text / per type | Answered | "The bank could not be reached. No money was moved." |
| 3 | Severity of `STUCK_TRANSACTION` | CRITICAL withdrawal, WARNING top-up / always CRITICAL / always WARNING | Answered | CRITICAL withdrawal, WARNING top-up |
| 4 | Alerting signal for stuck transactions | gauge / log only | Answered | Gauge `recon_stuck_transactions{type}` |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | `outbox.ListDead` + `outbox.Resolve` (DEAD → DONE with a note), resolved in the same tx as the business change | Yahya | this task |
| 2026-11-10 | `BANK_UNAVAILABLE` message "The bank could not be reached. No money was moved." for both types | Yahya | this task |
| 2026-11-10 | `STUCK_TRANSACTION` severity CRITICAL for withdrawals, WARNING for top-ups | Yahya | this task |
| 2026-11-10 | Gauge `recon_stuck_transactions{type}`, set every run (0 when none) | Yahya | this task |
| 2026-11-10 | Env vars `RECON_DEAD_*`, `RECON_STUCK_*` with TDR-009 values as defaults; age from `created_at`; run FAILED only when every bank call failed (as T022) | planning (follows the T021/T022 decisions) | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | In Review | Implemented; verification passed (implementation.md) |
| 2026-11-10 | Approved | Review round 1: 1 Nit (review.md) |
| 2026-11-10 | Done | Finalized — PR opened |

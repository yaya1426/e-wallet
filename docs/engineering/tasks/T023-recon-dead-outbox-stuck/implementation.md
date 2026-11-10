# Implementation — T023 Reconciliation jobs `dead_outbox` and `stuck_alert`

## Summary
**Outbox API:**
- `outbox.ListDead(ctx, q, types, limit)` lists DEAD events of the given types, oldest first.
- `outbox.Resolve(ctx, q, eventID, note)` moves DEAD → DONE, with "resolved by reconciliation: " + note in `last_error`. It returns `ErrNotDead` when the event is no longer DEAD.

**`payment.FailUnreachable(ctx, tx, txnID)`:** locks the transaction. If it is still PENDING:
- top-up → FAILED;
- withdrawal → `failAndRefund`.

Both use `BANK_UNAVAILABLE` and "The bank could not be reached. No money was moved." Otherwise it returns `ErrStatusConflict` and changes nothing.

**`dead_outbox`** (every `RECON_DEAD_INTERVAL`, up to `RECON_DEAD_BATCH`) handles DEAD `topup.requested` and `withdrawal.requested` events. Per event, with no DB transaction open during the bank call:
- **Transaction missing:** resolve the event (and log an error).
- **Already moved on:** resolve the event with the note "already <STATUS>", without asking the bank.
- **Bank has no record (`NOT_FOUND`):** `FailUnreachable`, resolve and `MarkReconciled`, all in one transaction.
- **Other bank error:** skip; the event stays DEAD for the next run.
- **Bank has it, still PENDING:** PENDING → PROCESSING with the bank's reference, resolve. `pending_bank_ops` takes over from there.
- **Final answer:** `ApplyBankOutcome`, resolve.

The run is FAILED only when every bank call of a non-empty batch failed. Two concurrent runs can't apply the same change twice: the second one hits `ErrStatusConflict` or `ErrNotDead`, and its transaction rolls back.

**`stuck_alert`** (every `RECON_STUCK_INTERVAL`) looks at top-ups and withdrawals still PENDING or PROCESSING more than `RECON_STUCK_AGE` after creation, up to 1 000 per run:
- each gets one open `STUCK_TRANSACTION` discrepancy: CRITICAL for a withdrawal, WARNING for a top-up;
- the gauge `recon_stuck_transactions{type}` is set to the full count per type, including 0, so the alert clears;
- it logs a WARN line when the count is above 0;
- money and status never change.

Both jobs are registered in `serve` and in `wallet reconcile`. The jobs' dependency struct `PendingBankOpsDeps` was generalised to `reconciliation.Deps` (adds `Outbox` and the `Stuck` observer).

## Files
| Path | Change |
|------|--------|
| `internal/outbox/dead.go`, `queries.sql`, sqlcgen | `DeadEvent`, `ListDead`, `Resolve`, `ErrNotDead` |
| `internal/payment/unreachable.go` | `FailUnreachable`, `CodeBankUnavailable` |
| `internal/transaction/queries.sql`, `service.go`, sqlcgen | `ListStuck`, `CountStuck` |
| `internal/reconciliation/dead_outbox.go`, `stuck_alert.go` | The jobs |
| `internal/reconciliation/pending_bank_ops.go` | `Deps` (renamed and extended), `StuckObserver`, shared `jobTimeout` |
| `internal/platform/config` | `RECON_DEAD_INTERVAL` (5m), `RECON_DEAD_BATCH` (100, 1..1000), `RECON_STUCK_INTERVAL` (15m), `RECON_STUCK_AGE` (24h) |
| `internal/platform/metrics` | `recon_stuck_transactions{type}` |
| `cmd/wallet/app.go` | Both jobs registered; `Outbox` and `Stuck` in the deps |
| `configs/.env.example` | The four variables |
| Tests | `internal/outbox/dead_integration_test.go`, `internal/payment/unreachable_integration_test.go`, `internal/reconciliation/dead_outbox_integration_test.go` (also `stuck_alert`), fixture extended with PENDING requests and dead events; `config_test.go`; the CLI test covers all three jobs |

No migration: the existing `transactions_open_status_idx` and `outbox_events_due_idx` cover the queries, and DEAD rows are few.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1, AC-2 | `TestDeadOutbox_BankHasNoRecord/TOPUP`, `/WITHDRAWAL` (FAILED `BANK_UNAVAILABLE` + message; withdrawal refunded; event DONE with note; no discrepancy), `TestFailUnreachable` | ✅ |
| AC-3 | `TestDeadOutbox_BankHasItPending` (PROCESSING with the bank's reference; then `pending_bank_ops` completes it) | ✅ |
| AC-4 | `TestDeadOutbox_BankHasFinalAnswer` (top-up succeeded → credited; withdrawal failed → bank's code and refund) | ✅ |
| AC-5 | `TestDeadOutbox_AlreadyMovedOn` (resolved "already COMPLETED", no bank call, credited once) | ✅ |
| AC-6 | `TestDeadOutbox_TemporaryErrors` (stays DEAD, retried next run; the other is handled; all calls failing → run error) | ✅ |
| AC-7 | `TestDeadOutbox_ResolvedOnce` (two concurrent runs → fixed once, one refund; then nothing left), `TestOutbox_ListDeadAndResolve` (`ErrNotDead`, a rolled-back resolve stays DEAD) | ✅ |
| AC-8 | `TestDeadOutbox_OnlyRequestEvents`, `TestOutbox_ListDeadAndResolve` (type filter) | ✅ |
| AC-9 | `TestStuckAlert` (two runs → one open row each; WARNING top-up / CRITICAL withdrawal; details; young and final ones not flagged; gauge 1/1, then 0/0 once final; no money or status change) | ✅ |
| AC-10 | `TestConfig_ReconDeadStuck`, `TestReconcile_CLI_PendingBankOps` (all three jobs run once from the CLI) | ✅ |
| AC-11 | `ledgertest.AssertIntegrity` and `paymenttest.AssertInTransit` in every reconciliation test's cleanup | ✅ |
| AC-12 | Compose walkthrough (below) | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 lint issues |
| Full suite `-race -tags integration` | ✅ 705 passed, 0 failed |
| Coverage | `reconciliation` 90.0%, `outbox` 94.1% |
| `make e2e` (normal stack) | ✅ |

### AC-12 — dead request on compose
1. **Bank unreachable.** Override: `BANK_BASE_URL=http://banksim:1`, `OUTBOX_MAX_ATTEMPTS=1`, `RECON_ENABLED=false`. A top-up of 50 000 got `202 PENDING`, and its `topup.requested` went `DEAD` after 1 attempt ("bank: temporary failure: … connection refused"). Banksim logged no request.
2. **Bank back.** The wallet was restarted with the normal `BANK_BASE_URL` and `RECON_DEAD_INTERVAL=5s`. After 4.6 s the top-up was `FAILED BANK_UNAVAILABLE`, "The bank could not be reached. No money was moved.", with a balance of 0.
   - Event: `DONE`, "resolved by reconciliation: bank has no record; BANK_UNAVAILABLE".
   - `job_runs`: `dead_outbox | SUCCESS | 1 | 1`.
   - `recon_job_runs_total{job="dead_outbox",status="success"} 1`.

The stuck gauge wasn't observed live: its first tick comes after a 15-minute interval. It is covered by `TestStuckAlert`.

## Deviations
- `PendingBankOpsDeps` was renamed to `Deps` and extended (all jobs share it). No behaviour change.
- No new index: see Files.

## Follow-ups
- P2-5: the operator API for discrepancies, which is where the stuck and unavailable cases are reviewed.

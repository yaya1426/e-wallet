# Implementation — T022 Reconciliation job `pending_bank_ops`

## Summary
`reconciliation.PendingBankOps(deps, cfg)` is registered with the T021 scheduler in `serve` and in `wallet reconcile`. On each run it lists top-ups and withdrawals that meet all of these:
- `PROCESSING`;
- created more than `RECON_PENDING_MIN_AGE` ago;
- not checked within `RECON_PENDING_RECHECK`.

It takes up to `RECON_PENDING_BATCH` of them, oldest first. For each one, with no DB transaction open during the bank call, it calls `bank.GetOperation(transaction ID)`:
- **Final answer** → in one transaction, `payment.ApplyBankOutcome` (the webhook's code) and `MarkReconciled`; *fixed* if the status became final.
- **Bank still `PENDING`** → `MarkReconciled` only.
- **`NOT_FOUND`** → in one transaction, lock the row and, if it is still PROCESSING, record a CRITICAL `STATUS_MISMATCH` (once while open), then `MarkReconciled`. No status or money change.
- **Any other error** → skipped and retried next run (`last_reconciled_at` unchanged). The run is FAILED only if every bank call of a non-empty batch failed.

`MarkReconciled` sets `last_reconciled_at` only (not `updated_at`).

The `reconcile` command now requires `BANK_BASE_URL`, because the jobs ask the bank.

## Files
| Path | Change |
|------|--------|
| `internal/reconciliation/pending_bank_ops.go` | The job |
| `internal/transaction/queries.sql`, `service.go`, sqlcgen | `ListForReconciliation`, `MarkReconciled` |
| `migrations/000011_transactions_recon_idx.up/down.sql` | Partial index `(created_at) WHERE status = 'PROCESSING'` |
| `internal/platform/config` | `RECON_PENDING_INTERVAL/MIN_AGE/RECHECK/BATCH` with validation; `ValidateBank()` (also used by `ValidateServe`) |
| `cmd/wallet/app.go` | `reconJobs` registers the job with its dependencies; `reconcile` checks `BANK_BASE_URL` and always builds the bank client |
| `.golangci.yml` | `reconciliation-deps` (denies identity, wallet, transfer, idempotency, webhook, banksim), the rule T021's review deferred |
| `configs/.env.example` | The four variables |
| Tests | `internal/reconciliation/pending_bank_ops_integration_test.go`; `config_test.go`; `cmd/wallet` (`TestReconcile_CLI` now also checks the `BANK_BASE_URL` requirement, `TestReconcile_CLI_PendingBankOps`) |

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestPendingBankOps_TopUpSucceeded` (COMPLETED, one `TOPUP_SETTLE`, balance 50 000, `last_reconciled_at` = now, checked 1 / fixed 1) | ✅ |
| AC-2 | `TestPendingBankOps_Outcomes` (top-up failed; withdrawal paid → reserve + settle; withdrawal failed → reserve + refund, balance back), with the in-transit invariant in cleanup | ✅ |
| AC-3 | `TestPendingBankOps_StillPending` (only `last_reconciled_at`, `updated_at` unchanged; no second call after 4 min; asked again after 6 min and applied) | ✅ |
| AC-4 | `TestPendingBankOps_Selection` (batch of 2, oldest first, then the next two; PENDING never asked; younger than `MIN_AGE` not asked) | ✅ |
| AC-5 | `TestPendingBankOps_NotFound` (two runs → one open CRITICAL `STATUS_MISMATCH` with the reason; withdrawal still PROCESSING, money still reserved) | ✅ |
| AC-6 | `TestPendingBankOps_TemporaryErrors` (one temporary error: the other is applied, run SUCCESS, the skipped one retried and applied next run; all calls fail → run error names the transaction) | ✅ |
| AC-7 | `TestPendingBankOps_AmountMismatch` | ✅ |
| AC-8 | `TestPendingBankOps_WebhookAlreadyApplied` (the job waits on the row lock held by a "webhook" that applies the outcome; the job's apply is a no-op; credited once, no discrepancy) | ✅ |
| AC-9 | `TestConfig_ReconPending`, `TestReconcile_CLI` (`BANK_BASE_URL` required), `TestReconcile_CLI_PendingBankOps` | ✅ |
| AC-10 | `ledgertest.AssertIntegrity` and `paymenttest.AssertInTransit` in the fixture's cleanup | ✅ |
| AC-11 | Compose walkthrough (below) | ✅ |

The type filter (`TOPUP`, `WITHDRAWAL`) can't be exercised directly: a P2P is created COMPLETED and can never be PROCESSING. The filter is in the SQL as a guard.

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 lint issues |
| Full suite `-race -tags integration` | ✅ 687 passed, 0 failed |
| Coverage `internal/reconciliation` | 91.8% |
| `make e2e` (normal stack) | ✅ |

### AC-11 — lost webhook on compose
A throwaway override pointed banksim's `BANKSIM_WEBHOOK_URL` at `/webhooks/lost` and set `RECON_PENDING_INTERVAL=5s`, `MIN_AGE=10s`, `RECHECK=5s`. A top-up of 50 000 (…0000):
- **t+0 s:** `PROCESSING`. Banksim logged "webhook failed; will retry" ×4 (404), and the wallet's `inbox_events` stayed empty.
- **t+7.6 s:** `COMPLETED`, with a balance of 50 000.
- **`job_runs`:** `pending_bank_ops | SUCCESS | checked 1 | fixed 1`.
- **Discrepancies:** 0.

## Deviations
None. The deferred `reconciliation-deps` lint rule from T021 is added here.

## Follow-ups
- T023: `dead_outbox` (PENDING transactions whose bank event went DEAD) and `stuck_alert`.

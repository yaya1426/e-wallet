# Review — T022 (2026-11-10, round 1)
Verdict: Approved
Summary: The job follows TDR-009 §2–3 and the four planning decisions. It selects by `created_at` with a recheck window and makes bank calls outside any DB transaction. Final answers are applied only through `ApplyBankOutcome`, which locks the row and is idempotent, so a webhook racing with the job is harmless (tested). NOT_FOUND is recorded and never auto-fixed. One Nit.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/reconciliation/pending_bank_ops.go:112` | Observability | When a bank call succeeds but `ApplyBankOutcome` or `MarkReconciled` fails (a DB error), the run is still SUCCESS. The decided rule counts only bank-call failures, so this is correct, but such an error appears only in the logs, not in `job_runs.error`. | Optionally store non-fatal errors in `job_runs.error` even when the run is SUCCESS (needs a decision; not needed now). |

## Checked and fine
- **Architecture:**
  - `reconciliation` imports payment, transaction, bank, discrepancy and platform only.
  - The `reconciliation-deps` lint rule is now enforced.
  - `transaction` owns its new queries.
- **Money:**
  - No posting is done by the job; all of it goes through `ApplyBankOutcome` (settle, refund, discrepancies).
  - Ledger integrity and the in-transit invariant are asserted after every test.
- **Concurrency:**
  - The row lock is taken inside `ApplyBankOutcome`, and the `notFound` path locks before recording.
  - The webhook race test passes.
  - The advisory lock from T021 means one runner at a time.
- **Failure handling:**
  - A temporary error skips that transaction only, and a ctx timeout stops the batch cleanly.
  - The run fails only when the whole batch failed.
- **Data:**
  - The partial index matches the query.
  - `MarkReconciled` leaves `updated_at` alone (tested).
- **Config:** defaults equal TDR-009, with validated ranges.
- **Tests:**
  - Every AC is mapped; AC-11 passes live with real lost webhooks.
  - Coverage: 91.8%.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

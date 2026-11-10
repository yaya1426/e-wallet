# Review — T023 (2026-11-10, round 1)
Verdict: Approved
Summary: `dead_outbox` applies the TU-BR-7 / WD-BR-4 rule: a request is only failed with `BANK_UNAVAILABLE` after the bank confirms it has no record. Every business change and the outbox `Resolve` share one DB transaction, so a crash can't leave one without the other. Concurrent runs can't change the same transaction twice. `stuck_alert` only records and measures. All four planning decisions are implemented. One Nit.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/reconciliation/stuck_alert.go:18` | Plan conformance / scale | `stuck_alert` flags at most 1 000 transactions per run, oldest first; the gauge still counts all of them. With more than 1 000 stuck, the rest are flagged on later runs (every 15 min). This matches the task, and is mentioned here so operators know. | None now; make the limit configurable if it is ever reached. |

## Checked and fine
- **Architecture:**
  - `outbox` keeps owning its table behind `ListDead` / `Resolve`.
  - `payment` owns the failure rule (`FailUnreachable`, reusing `failAndRefund`).
  - `transaction` owns its queries.
  - `reconciliation` only orchestrates, and depguard stays clean.
- **Money:**
  - A withdrawal refund is posted only when the bank has no record or a final FAILED (WD-BR-4); a temporary error never refunds.
  - No posting outside `payment`.
  - Ledger integrity and the in-transit invariant are asserted after every test.
- **Concurrency:**
  - The `FailUnreachable` row lock and the `Resolve` "still DEAD" guard make concurrent runs safe (tested), on top of the advisory lock.
  - No network call runs inside a transaction.
- **Data and metrics:**
  - `Resolve` notes are readable in `last_error`.
  - The gauge is set to 0 when there are none, so alerts clear (tested).
- **Config:** the defaults equal TDR-009, and the values are validated.
- **Tests:**
  - Every AC is mapped; dead events are produced by a real outbox runner.
  - The handoff from `dead_outbox` to `pending_bank_ops` is tested end to end.
  - AC-12 passes live in two phases.
  - Coverage: 90%.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

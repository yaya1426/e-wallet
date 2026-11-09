# Review — T019 (2026-11-09, round 1)
Verdict: Approved
Summary: A small, focused change. Payout outcomes go through the same ordered rule table as debits, and only the final step differs by transaction type: settle a withdrawal on success, refund it on failure. The refund happens only on a confirmed failure, and a contradiction after a final state is recorded and never auto-fixed. Only one Nit, carried over.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/payment/outcome.go:180` | Performance (as T016 #3, T018 #2) | `settleWithdrawal` looks up two system accounts by code each time. | Cache the system account IDs once. |

## Checked and fine
- **B Money:**
  - `WITHDRAWAL_SETTLE` is Dr `WITHDRAWALS_IN_TRANSIT` / Cr `BANK_SETTLEMENT` (PRD-003 WD-BR-2, TDR-004 §3); the wallet is not touched again.
  - The refund only happens on a confirmed FAILED (WD-BR-4), with `failAndRefund` in the same transaction as the transition.
  - Terminal states never change (BR-15).
  - "Paid but ours FAILED" and "failed but ours COMPLETED" are CRITICAL `STATUS_MISMATCH` with no money change (PRD-003 §7).
  - An amount mismatch keeps the reserve.
  - The `(transaction_id, entry_type)` uniqueness backs up idempotency.
  - In-transit invariant: asserted in every test and live.
- **A / C:**
  - No new imports; the error that is no longer needed is removed.
  - The final `switch` over type × status reads as the rule table.
  - Doc comments are updated; gofmt, vet and golangci-lint report 0 issues.
- **E Tests:**
  - AC-1…AC-8 are mapped, including both orders of contradiction and both early-webhook outcomes.
  - The T016 tests were updated, not weakened: they now assert the applied result.
  - The live walkthrough covers all three scenarios.
- **Plan conformance:**
  - The two files listed in the task were changed, and `refund.go` was reused unchanged.
  - Nothing else was added.

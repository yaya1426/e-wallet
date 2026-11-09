# Review — T016 (2026-11-09, round 1)
Verdict: Approved
Summary: `ApplyBankOutcome` follows the task's rule table in the specified order (kind → reference → terminal → amount → PENDING step → settle/fail), all inside the caller's transaction after a row lock. It never auto-fixes: every contradiction becomes a discrepancy, with no money or status change. The discrepancy module is minimal and owns its table. One Minor and two Nits remain.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | `internal/webhook/service.go:167` | TDR-009 §3 (one record per problem) | `UNKNOWN_BANK_EVENT` rows have no `transaction_id`, so the partial unique index does not dedupe them. If the runner's DEAD mark is stale (its lease expired and another worker re-claimed the event), the last attempt runs again and records a second row. This is rare: it needs a handler slower than the 60 s lease. The impact is a duplicate WARNING row. | Dedupe on `details->>'event_id'` (e.g. a partial unique index on `(kind, (details->>'event_id')) WHERE kind = 'UNKNOWN_BANK_EVENT'`) in a later migration. |
| 2 | Nit | `internal/payment/outcome.go:183` | Design note | The kind-mismatch, reference-mismatch and contradiction cases all use `STATUS_MISMATCH`. Because only one open row is kept per (transaction, kind), a second, different status problem on the same transaction is only logged until the first is resolved. This is how the task specifies it; it is mentioned here so operators know. | None, or put "reason" in the operator UI (phase 2). |
| 3 | Nit | `internal/payment/outcome.go:154` | Simplicity / performance | `BANK_SETTLEMENT` is looked up by code for every settled top-up, although its ID is fixed by the migration. | Optionally cache the ID in `Service`. |

## Checked and fine
- **A Architecture:**
  - Imports match the plan: `discrepancy` imports only platform; `payment` imports wallet, transaction, ledger, bank, discrepancy and webhook (the deviation is documented; there is no cycle); `webhook` imports discrepancy.
  - depguard rules were added and lint is clean.
  - Each module touches only its own tables through other modules' services.
  - No HTTP types are used in `payment` or `discrepancy`.
- **B Money:**
  - The only posting is `ledger.Post(TOPUP_SETTLE)` with Dr BANK_SETTLEMENT / Cr user wallet (TDR-004 §3), in the same transaction as the COMPLETED transition.
  - The state machine is used for every change (`Transition(from, to)`).
  - Terminal states never change (BR-15).
  - The amount check runs before any transition (TU-BR-3).
  - FROZEN and max-balance wallets are still credited (BR-8, TU-BR-4).
  - `ErrDuplicateEntry` is surfaced, not swallowed.
  - No network call runs inside a transaction.
  - Concurrent duplicates are serialized by `GetForUpdate` (tested ×3 with 5 goroutines).
- **C Go standards:**
  - Doc comments explain the rules and the reasons for them.
  - Errors are sentinels wrapped with `%w`.
  - `webhook.NewService` refuses a nil recorder.
  - Log attributes carry no secrets.
  - gofmt, vet and golangci-lint report 0 issues.
- **D Data:**
  - The migration matches the task's DDL (CHECKs, FK to `transactions`, partial unique index, status index).
  - `ON CONFLICT DO NOTHING` covers the partial index.
  - `List` validates its filter and sorts by `created_at DESC, id DESC`.
- **E Tests:**
  - Every AC has an integration test, with ledger integrity checked in cleanup.
  - The whole pipeline is tested: signed webhook → inbox → payment.
  - Live compose run: completed, failed, amount mismatch.
  - Coverage: payment 90.0%, discrepancy 95.2%.
- **Plan conformance:**
  - Every file in the How table is done; `wallet.GetByID` already existed.
  - The documented deviations (payment imports webhook, the lock taken inside ApplyBankOutcome, the reference/key cross-check) stay inside the task's intent.
  - No payout logic and no operator API were added (out of scope).

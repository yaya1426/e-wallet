# Review — T018 (2026-11-09, round 1)
Verdict: Approved
Summary: The withdrawal reserves money with a real ledger entry in the same unit of work as the transaction and the outbox event. The checks follow the decided order, all under the wallet account lock. A synchronous bank rejection fails and refunds in one transaction, and only if the withdrawal is still PENDING; a timeout never refunds. The shared request/handler refactor removes the T017 duplication, including T017 review Minor #1. Two Nits remain.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/payment/topup.go:76` | C — simplicity | `decodePayload` is a one-line wrapper around `json.Unmarshal` and sits in `topup.go`, although only `request.go` uses it. | Call `json.Unmarshal` in `deliverRequested` directly. |
| 2 | Nit | `internal/payment/withdrawal.go:59` | Performance (same as T016 Nit #3) | The system accounts (`WITHDRAWALS_IN_TRANSIT`, `BANK_SETTLEMENT`) are looked up by code on every request, settlement and refund, although their IDs are fixed by the migration. | Cache the IDs once in `Service` (e.g. lazily, or have `ledger` export them). |

## Checked and fine
- **A Architecture:**
  - Same imports as T017 (all allowed by `payment-deps`).
  - `paymenttest` imports only `ledger` and `platform`.
  - `cmd/wallet` is unchanged: `OutboxHandlers()` registers the new event.
  - No HTTP types are used outside `http.go`.
- **B Money:**
  - `WITHDRAWAL_RESERVE` is Dr user / Cr `WITHDRAWALS_IN_TRANSIT` and `WITHDRAWAL_REFUND` the reverse (TDR-004 §3).
  - The balance is checked under `LockAccounts` before the reserve (TDR-010), so 10 parallel withdrawals give exactly 5 accepted and never a negative balance.
  - The daily limit uses `SumOutgoingForDay` on the Cairo day, so FAILED withdrawals are excluded.
  - Rejection → FAILED and refund happen in one transaction after a row lock; a redelivered rejection does nothing.
  - Temporary failure → Retry with the same key and no refund (WD-BR-4/5).
  - There is no network call inside a transaction.
  - The refund credits a frozen wallet (WD-BR-8).
  - Invariant 5 is asserted after every payment test and every withdrawal API test, and live.
- **C Go standards:**
  - `bankFlow` keeps the handler readable; doc comments state the rules.
  - Errors are wrapped; `isBusinessError` and `render` share one table.
  - gofmt, vet and golangci-lint report 0 issues.
- **D API:**
  - The check order and codes match the task's table, and the OpenAPI entry lists all ten checks.
  - The 202 body matches PRD-003 §6.
- **E Tests:**
  - AC-1…AC-9 are mapped, including the concurrency test.
  - The daily limit is tested with real P2P transfers and a real bank rejection through the outbox.
  - The race in which a rejection arrives after the withdrawal moved on is tested.
  - Coverage: payment 86.1%.
- **Plan conformance:**
  - Every file in the How table is done; `limits.go` and `cmd/wallet` needed no change.
  - Payout webhooks are left to T019, as planned. In the live run they wait in the inbox, which is expected.

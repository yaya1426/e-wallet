# Review — T017 (2026-11-09, round 1)
Verdict: Approved
Summary: The top-up request mirrors the reviewed P2P pattern: idempotency Begin/Complete in one unit of work, checks before writes, business errors stored and replayed. It adds the outbox event, and wakes the outbox only after the commit. The outbox handler keeps the bank call outside any DB transaction, uses the transaction ID as the bank key, and treats "already moved on" as Done. Every AC is covered, and AC-9 passes live against banksim. One Minor and one Nit remain.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | `internal/payment/topup.go:39` | C — simplicity, one source of truth (as in `transfer`'s `businessErrors` table) | Which errors count as business errors (stored for the key) is listed in `topUpBusinessErrors`, while how they render lives in `http.go`'s `businessErrors`. If one list gains an error the other lacks, that error is either stored as a 500 or returned as an unexpected error. Today the two lists match. | Derive `topUpBusinessErrors` from one shared table, e.g. a `businessError` slice owned by the service with the HTTP layer only mapping codes. T018 should share it. |
| 2 | Nit | `cmd/wallet/topup_integration_test.go:245` | E — determinism | "Exactly one bank call" is asserted after a fixed 200 ms wait. | Acceptable; alternatively assert after the outbox row is DONE. |

## Checked and fine
- **A Architecture:**
  - `payment` imports bank, discrepancy, idempotency, identity, ledger, outbox, platform (clock, database, httpserver, money, queue), transaction, wallet and webhook. All of these are allowed by `payment-deps` and architecture §3.2.
  - It owns no table and uses other modules only through their services.
  - The handler has no SQL; HTTP types stay in `http.go`.
  - The gateway is injected (banksim client in serve, fake in tests); callers never see HTTP.
- **B Money and correctness:**
  - No ledger entry at request time (TU-BR-1).
  - The wallet's ledger account is locked before reading the balance and pending sum (decision of 2026-11-05).
  - Overflow-safe limit checks are reused from `wallet.Limits`.
  - The bank key is the transaction ID (TDR-005 L3).
  - There is no network call inside `InTx`.
  - Rejection vs temporary failure is classified by the T013 port.
  - The state machine is used for each change, and a conflict means Done (AC-6, race tested).
  - Frozen after acceptance still continues (TU-BR-8).
- **C Go standards:**
  - Doc comments explain why; errors are wrapped; the outbox wake happens after the commit (TDR-006 §2).
  - `Deps` keeps the constructor readable.
  - gofmt, vet and golangci-lint report 0 issues.
- **D API:**
  - The check order and codes match the task's table, and the OpenAPI entry, schemas and catalogue are updated.
  - The 202 body matches PRD-002 §6.
  - Strict JSON comes from `httpserver.DecodeJSON`.
- **E Tests:**
  - AC-1…AC-10 are mapped, with parallel tests for the same key and for the maximum balance.
  - The handler tests call the registered handler directly with `banktest`.
  - The real wiring is tested end to end (outbox → fake bank).
  - Live walkthrough: `…0000`/`…0001`/`…0002`.
  - Coverage: payment 87.1% across packages.
- **Plan conformance:**
  - Every file in the How table is done.
  - The `limits.go` functions already existed (the name differs from the task: `CheckTopUpBalance`).
  - The `Deps` constructor and the `Fail` cases are documented deviations.
  - No reconciliation work was done (out of scope).

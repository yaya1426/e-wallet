# Review — T006

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The transaction table, state machine and limit sums match the task and are thoroughly
tested. gofmt is clean, vet is ok, lint shows 0 issues, sqlc-check passes, 292 tests pass, and
`transaction` coverage is 89.7%. There's no Blocker, Major or Minor finding. One Nit (a wrong
comment in a test fixture) is fixed. I reviewed this alone, re-reading every file against the
checklist.

| # | Severity | File:line | Rule | Finding | Status |
|---|----------|-----------|------|---------|--------|
| 1 | Nit | `internal/transaction/service_integration_test.go:34` | C: comments | The comment said `a, b` were both "wallets of user", but the schema allows one wallet per user, so `b` belongs to a second user. | **Fixed** |

### Checked and fine
- **A Architecture:** `transaction` imports only `platform/{clock,id,money}` and its own sqlcgen (`go list`); the depguard rule is probed. The table is owned by `transaction`. The ledger tests' raw-SQL seeding is test-only and documented, since the ledger package may not import business modules.
- **B Correctness:**
  - `Transition` updates `WHERE status = from`. Under READ COMMITTED a concurrent second update waits, re-checks and gets 0 rows, then returns `*StatusConflictError`; this is tested with two goroutines.
  - An invalid pair returns `ErrInvalidTransition` without SQL.
  - A duplicate bank reference or idempotency key maps to its sentinel, and the docs say the DB transaction is aborted.
  - Terminal statuses set `completed_at`; P2P is created with it.
  - The sums' doc comments require the caller to lock the ledger account first.
  - No network calls; all times truncated to µs.
- **C Go:** the `ErrStatusConflict` sentinel plus `*StatusConflictError` with `Is`; wrapped errors; package doc; consumer-side `Querier`.
- **D Data:**
  - The migration matches the task, with named constraints; the FK on `journal_entries` was verified live; the partial index is for open statuses.
  - The sqlc nullable overrides apply only to this module's block.
  - Sum queries follow PRD-001 §5.6 / PRD-003 WD-BR-7 / PRD-002 §3.5.
- **E Tests:**
  - Every AC is covered, including the Cairo-day boundary (21:59:59Z vs 22:00:00Z), the concurrent transition, and the DB CHECK as a last guard.
  - Ledger integrity is asserted in the cleanup.
  - The existing ledger and wallet tests were moved to real transactions without weakening any assertion.

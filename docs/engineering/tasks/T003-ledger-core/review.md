# Review — T003

## Round 2 (2026-11-06)

Verdict: **Approved**

Summary: The Major and both Minor findings are fixed and tested. `make ci` passes (exit 0: tidy,
fmt, vet, lint 0 issues, sqlc-check, build, unit + integration); 160 tests pass with `-race`;
compose + e2e pass. The Nit is kept as designed.

| # | Round-1 severity | Status | Evidence |
|---|------------------|--------|----------|
| 1 | Major | **Fixed** | `Post` sorts with `compareLockOrder`: user-wallet accounts first, then system accounts, each by ascending ID (`service.go`). The fixed system IDs live in `model.go`; `TestSystemAccountIDs_MatchMigrationSeed` checks them against the DB seed. `TestPost_PreLockedAndUnlockedCreditsNoDeadlock` runs two transactions without retries and passes. With the old order the same test fails with `deadlock detected (SQLSTATE 40P01)`, so it does detect the bug. The `queries.sql` comment is corrected, and TDR-010 §3 has a dated clarification. Also `TestCompareLockOrder_UserWalletsBeforeSystem`. |
| 2 | Minor | **Fixed** | Zero-value/unsupported currency now wraps `money.ErrUnsupportedCurrency` (`TestEntryValidate` "zero-value money") |
| 3 | Minor | **Fixed** | `ErrMissingTransactionID` (`TestEntryValidate` "nil transaction"), `ErrMissingWalletID` (`TestCreateUserAccount_NilWalletID`) |
| 4 | Nit | Kept | As suggested |

New code reviewed against the checklist: no new findings.

## Round 1 (2026-11-06)

Verdict: **Changes Requested**

Summary: The ledger is solid. The schema, triggers, sentinel errors, sqlc setup and tests match
the task; gofmt is clean, vet is ok, golangci-lint shows 0 issues and `sqlc-check` passes; all
155 tests (unit + integration, `-race`) pass. One Major design issue: the order in which `Post`
takes row locks can deadlock against callers that pre-lock user accounts. It rests on a wrong
premise in TDR-010 §3 and in the `queries.sql` comment. Two Minor validation gaps.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | **Major** | `internal/ledger/service.go:111-113`, `internal/ledger/queries.sql` (ApplyPosting comment), TDR-010 §3 | Checklist B (row locks, lock order), TDR-010 §1-2 | `Post` applies postings in ascending account-ID order. The seeded system accounts have the lowest IDs (`00000000-…`), so their `UPDATE` always runs first. In PostgreSQL an `UPDATE`'s row lock is held **until commit**, not "only for this statement" as the comment and TDR-010 §3 say. A caller that does `LockAccounts([user])` and then `Post` takes locks in user → system order; a caller that `Post`s a credit to the same user without pre-locking takes them in system → user order. Run concurrently, the two deadlock (40P01). `InTx` retries it, but the money path depends on that retry, and every bank flow serialises on the system row until commit without the docs saying so. | Use one global order: **user-wallet accounts first (ascending ID), then system accounts (ascending ID)**. The system IDs are fixed in the migration, so `ledger` can know them as constants (checked against the DB by a test). Correct the comment, document the real behaviour in TDR-010 §3, and add a test without retries that reproduces the pre-lock vs. no-pre-lock case. |
| 2 | Minor | `internal/ledger/model.go:151-155` | Errors (checklist C) | If the first posting's `Money` is the zero value or has an unsupported currency, `validate` returns `ErrCurrencyMismatch` ("postings use different currencies"), which is misleading. | Wrap `money.ErrUnsupportedCurrency` with the posting index instead. |
| 3 | Minor | `internal/ledger/model.go:144`, `service.go:35` | Validation | `Entry.TransactionID == uuid.Nil` and `CreateUserAccount(walletID == uuid.Nil)` are accepted, so an entry could be written against a nil transaction (there is no FK until T006). | Reject nil IDs with sentinel errors before any SQL. |
| 4 | Nit | `internal/ledger/repository.go:17-21` | Simplicity | `Querier` repeats sqlc's `DBTX`. That's acceptable, since `sqlcgen` is Go-internal and can't be exported. | Keep it. |

### Checked and fine
- **A Architecture:** placement per TDR-012/the task; `ledger` imports only `platform/*` and its own Go-internal `sqlcgen` (verified with `go list`); depguard `ledger-no-business` added; no other module can reach the ledger's tables.
- **B Money:**
  - Validation before SQL (≥ 2 postings, > 0, one currency, balanced via `money.Add` with overflow check, known type).
  - Signed delta by normal side.
  - `balance_after` returned by the same `UPDATE`.
  - `CHECK` → `ErrInsufficientBalance`, unique `(transaction_id, entry_type)` → `ErrDuplicateEntry`.
  - Deferred balanced-entry triggers on both tables; append-only and no-truncate triggers.
  - `VerifyIntegrity` uses single-statement queries, each consistent on its own.
- **C Go standards:** naming, doc comments, sentinel errors wrapped with `%w`, no floats.
- **D Data:** migration pair per TDR-014; fixed seed IDs; constraint names used by the error mapping match the migration; `sqlc.yaml` per module with Go-internal output; `sqlc-check` in CI and `make ci`.
- **E Tests:** every AC has a test (23 ledger tests plus the migration-version updates); the integrity check runs after every ledger integration test.

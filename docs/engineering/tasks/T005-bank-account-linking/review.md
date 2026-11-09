# Review — T005

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The task does what it says and is well tested. gofmt is clean, vet is ok, lint shows 0
issues, sqlc-check passes, 262 tests pass, and curl + e2e pass. One Minor layering issue (fixed
during the review, since it's a two-line change) and one Nit. I reviewed this small change alone,
without a second reviewer.

| # | Severity | File:line | Rule | Finding | Suggested fix / status |
|---|----------|-----------|------|---------|------------------------|
| 1 | Minor | `internal/identity/http.go:153` | A: handlers → service → repository; no abstraction leakage | `getBankAccount` called `h.svc.GetBankAccount(ctx, h.svc.db.Pool(), …)`, so the HTTP handler reached into the service's database pool. | **Fixed**: `Service.LinkedBankAccount(ctx, userID)` reads on the pool; the handler calls it. Build, vet and the bank-account tests pass again. |
| 2 | Nit | `internal/identity/http.go` `requestUser`, `internal/wallet/http.go` | Simplicity | Both modules repeat the same "no user in context → 500" lines. | Optional: a shared helper in `httpserver` once a third module needs it. |

### Checked and fine
- **A Architecture:** `bank_accounts` is owned and queried by `identity` only; imports unchanged (`identity` → platform only, `go list`); routes are behind `RequireAuth`, so every call uses only the token's user (AC-5 test).
- **B Money:** none involved. IBAN validation is integer-only (mod-97 digit by digit).
- **C Go:** naming (`NormalizeIBAN`, `ErrInvalidIBAN`, `ErrBankAccountNotFound`), wrapped errors, and no IBAN in error text or logs (live check: 0 matches).
- **D Data/API:**
  - The migration matches the task (unique `user_id`, CHECK); the upsert keeps `id`/`created_at`.
  - Response and error codes match the task table and OpenAPI; the catalogue test covers them.
  - Timestamps truncated to µs.
- **E Tests:** every AC has unit/integration/HTTP tests, and validation order is tested. The migration-version tests now derive the latest version from the files, so the check is kept and not hard-coded.

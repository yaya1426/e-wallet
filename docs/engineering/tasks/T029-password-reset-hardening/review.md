# Review — T029 (2026-11-12, round 1)
Verdict: Approved
Summary: The change does what TDR-021 and the task say, inside the identity module only. The
access-token cut-off reuses the user row `Authenticate` already loads, so there is no extra query.
Tests cover every AC, including concurrency. Two optional Minor findings.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | internal/identity/model.go:31 | Abstraction leakage (B) | `User.PasswordChangedAt` is visible to other modules through `identity.User` (e.g. transfer's `GetUserByPhone`). It is harmless, and no handler serialises `User` directly. | Optional: keep it on an internal row type if `User` grows more auth-only fields. |
| 2 | Minor | internal/identity/reset.go:115 | Concurrency (TDR-010) | The disabled check reads `users` without a lock, so an operator disabling a user at the same instant can race a reset. The window is milliseconds, and login stays blocked for a disabled user. | Optional: `SELECT … FOR SHARE` on the user if disabling becomes an automated flow. |

## Checked and fine
- Boundaries: only `internal/identity` and its own tables (`users`, `password_reset_codes`, `refresh_tokens`). Imports unchanged.
- Transactions: password hash, `password_changed_at`, `used_at` and refresh-token revocation are in one `InTx`. bcrypt runs before the tx, and there's no network I/O inside it.
- Security: same 401 body for every reset failure, disabled included. Constant-time compare unchanged. Tokens without `iat` are now rejected. The 1-second window is documented (TDR-021 §1).
- Go: naming, wrapped errors (`%w`), `slog` attrs, doc comments updated. gofmt, vet and golangci-lint are clean.
- Data/API: the forward-only migration is nullable with no backfill and no table rewrite. sqlc is regenerated. OpenAPI 403 is documented and tested.
- Tests: deterministic (fake clock), `-race`, parallel goroutines for AC-4/AC-5. T027 tests are untouched.
- Plan conformance: matches task.md, with nothing extra beyond the wrong-code log line written into TDR-021 §3.

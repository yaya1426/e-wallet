# T029 — Password reset hardening

| Field        | Value |
|--------------|-------|
| ID           | T029 |
| Status       | Approved |
| Created      | 2026-11-12 |
| Branch       | `task/T029-password-reset-hardening` |
| Depends on   | T027 (password reset), T025 (sessions) |
| PRDs         | PRD-001 US-8 |
| TDRs         | TDR-011, TDR-018 §3–5, TDR-021 |
| Docs changed | TDR-021 (new), TDR-018 (status: amended by TDR-021), docs/README.md, data-model.md `users` |

## What
Close the gaps found when reviewing T027 so a password reset really locks out whoever had the
old credentials, and prove the edge cases with tests.

- A reset (and any future password change) ends **access tokens** too, not only refresh tokens.
- Reset codes cannot be issued for, or used by, a `DISABLED` user.
- Operator issue and user reset are auditable (user id in the log line).
- New tests: concurrency (double submit, parallel wrong guesses), access token after reset,
  disabled user, response parity for unknown phone vs wrong code.

## Why
Review of T027 (2026-11-12) against TDR-018 §4:
1. `RevokeAllSessions` revokes refresh tokens only; an attacker holding a stolen access token
   keeps access for up to 15 min after the victim resets (refresh.go:140, documented, not fixed).
2. `IssueResetCode` / `ResetPassword` ignore `users.status`: a disabled user can get a code and
   change the password (login is still blocked, but the operator flow should refuse).
3. Log lines `password reset code issued` / `password reset` carry no user id, so an operator
   action cannot be traced to an account.
4. Concurrency of the `FOR UPDATE` path and the 5-attempt cap is correct by reading, but untested.

## How
### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000014_users_password_changed_at` | Column below |
| `internal/identity/queries.sql` + sqlcgen | `UpdatePasswordHash` also sets `password_changed_at`; `GetUser` returns it |
| `internal/identity/service.go` | `Authenticate` rejects tokens issued before `password_changed_at` |
| `internal/identity/reset.go` | Disabled-user checks; user id in logs |
| `internal/identity/http.go` | Internal endpoint returns `ErrUserDisabled` via the existing mapping (403) |
| `api/openapi/wallet.yaml` | 403 `USER_DISABLED` on the internal endpoint; 401 note on reset |
| `cmd/wallet/password_reset_integration_test.go` | New tests |
| `docs/tdrs/TDR-021-password-reset-hardening.md`, `docs/engineering/data-model.md` | Done while planning |

### Data (migrations)
`ALTER TABLE users ADD COLUMN password_changed_at timestamptz NULL;` (NULL = never changed;
no backfill). Down migration drops it.

### API
- `POST /v1/auth/password-reset`: unchanged contract. A disabled user → same `401
  INVALID_RESET_CODE` (no account-state leak).
- `POST /internal/v1/users/{phone}/password-reset-code`: disabled user → `403 USER_DISABLED` (existing mapping).
- Any authenticated endpoint: an access token issued before the user's last password change →
  `401 UNAUTHENTICATED` (existing code).

### Behaviour and design notes
- **Access-token cut-off.** JWT `iat` has 1-second precision. `Authenticate` rejects when
  `password_changed_at IS NOT NULL AND iat < password_changed_at truncated to the second`.
  Tokens minted in the same second as the reset survive (≤ 1 s window, accepted in TDR-021).
  No extra query: `Authenticate` already loads the user. `verify` must return `iat`.
- `UpdatePasswordHash` sets `password_changed_at = now` in the same tx as the reset (already one tx).
- **Disabled user.** `IssueResetCode`: after loading the user, `DISABLED` → `ErrUserDisabled`
  (no code row written). `ResetPassword`: `DISABLED` → `ErrInvalidResetCode`, no attempt counted.
- **Logs.** Add `slog.String("user_id", …)` to the issue and reset lines, and a new
  `password reset: wrong code` line with `user_id` (TDR-021 §3); still never the phone, code or password.
- Rate limiting stays as in T027 (shared login limiter).

### Out of scope
SMS/email delivery; self-service "forgot password" request endpoint; peppered (HMAC) code hash,
dedicated reset limiter and purge of old code rows (Q3); distributed rate limiting.

## Acceptance Criteria
- [x] AC-1 — Given a user with a valid access token A, when they reset their password, then A → 401 `UNAUTHENTICATED` on `GET /v1/wallet`, and a token from a fresh login works.
- [x] AC-2 — Given no password change ever, existing access tokens keep working (NULL column).
- [x] AC-3 — Given a disabled user, the operator issue endpoint → 403 `USER_DISABLED` and no row is written; a reset with a code issued before disabling → 401 `INVALID_RESET_CODE` and the password is unchanged.
- [x] AC-4 — Given one valid code, when two resets with it run concurrently, then exactly one → 204 and the other → 401.
- [x] AC-5 — Given one valid code, when 10 wrong guesses run concurrently, then `attempts` ends at 5, the code is invalidated, and the right code then → 401.
- [x] AC-6 — Unknown phone, wrong code, expired code and disabled user all return byte-identical 401 bodies.
- [x] AC-7 — Log lines for issue and reset contain `user_id` and never the code, phone or password.
- [x] AC-8 — OpenAPI lists 403 `USER_DISABLED` on the internal endpoint.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2 | integration HTTP (cmd/wallet) | `TestPasswordReset_RevokesAccessTokens`, `TestAuthenticate_NoPasswordChange` |
| AC-3 | integration HTTP | `TestPasswordReset_DisabledUser` |
| AC-4, AC-5 | integration (testcontainers, goroutines) | `TestPasswordReset_ConcurrentUse`, `TestPasswordReset_ConcurrentWrongGuesses` |
| AC-6 | integration HTTP | `TestPasswordReset_UniformFailures` |
| AC-7 | unit (slog capture handler) | `TestResetLogs_NoSecrets` |
| AC-8 | unit | OpenAPI catalogue |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 0 | Scope of "backend proofing" | Core hardening (Rec.) / core + defense-in-depth / tests only | Answered | Core hardening |
| 1 | How to cut off access tokens | `password_changed_at` vs JWT `iat` (Rec.) / `token_version` claim / accept 15-min window | Answered | `password_changed_at` vs `iat` |
| 2 | Disabled users and reset | Refuse issue (403, existing code) and reset (401) (Rec.) / allow | Answered | Refuse |
| 3 | Extra defense-in-depth now? | Defer (Rec.) / include HMAC pepper, dedicated reset limiter, purge of old codes | Answered | Defer (follow-up) |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Core hardening scope, recommended answers to Q1–Q3; take every task to production ready | Yahya | TDR-021, TDR-018, docs/README.md, data-model.md |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Draft | Created by plan-new-task; waiting on scope and Q1–Q3 |
| 2026-11-12 | Ready | All questions answered; TDR-021 written |
| 2026-11-12 | In Progress | Implementation started on `task/T029-password-reset-hardening` |
| 2026-11-12 | In Review | Implemented; full verification passed (implementation.md) |
| 2026-11-12 | Approved | Review round 1: Approved, 2 optional Minor findings (review.md) |

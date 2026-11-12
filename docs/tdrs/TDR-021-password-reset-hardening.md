# TDR-021 — Password Reset Hardening

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-12) |
| Date       | 2026-11-12 |
| Deciders   | Yahya Elaraby |
| Related    | Amends TDR-018 §4 (password reset) and TDR-011 (access tokens); PRD-001 US-8; T029 |

## Context
A review of the T027 password reset (TDR-018 §4) found three gaps:
1. A reset revokes the user's refresh tokens only. An access token that was already issued (for
   example a stolen one) keeps working for up to 15 minutes after the victim resets.
2. A reset code can be issued for, and used by, a `DISABLED` user.
3. The operator's "issue code" and the user's "reset" log lines carry no user id, so an action
   cannot be traced to an account.

## Decision
### 1. A password change ends access tokens
- New nullable column `users.password_changed_at timestamptz` (NULL = never changed; no backfill).
- Every password change (today only the reset) sets it to `now`, in the same transaction as the
  new hash.
- `Authenticate` (which already loads the user on every request) rejects an access token whose
  `iat` is **before** `password_changed_at` truncated to the whole second → `401 UNAUTHENTICATED`.
  JWT `iat` has one-second precision, so a token minted in the same second as the change still
  works: an accepted window of at most 1 second. No new claim, no change to the token format.

### 2. Disabled users cannot reset
- Operator `POST /internal/v1/users/{phone}/password-reset-code` for a `DISABLED` user →
  `403 USER_DISABLED` (existing code); no code row is written.
- User `POST /v1/auth/password-reset` for a `DISABLED` user → the same `401 INVALID_RESET_CODE`
  as every other failure (no account-state leak); no attempt is counted and the password is not changed.

### 3. Audit
The "code issued", "password reset" and "password reset: wrong code" log lines include
`user_id`. They still never include the phone, the code or the password.

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| `token_version` integer claim in the JWT | Exact, no 1-second window | Changes the token format; every token carries a new claim | The 1-second window is acceptable; no format change needed |
| Access-token deny list (jti) | Exact, per-token | New table, written on every reset, read on every request | More storage and code for the same result |
| Accept the 15-minute window (status quo) | No work | A stolen token outlives a reset | It is the main reason to reset |

## Consequences
- Positive: a reset signs the user out everywhere at once; operator actions are traceable.
- Negative / trade-offs: one nullable column; ≤ 1-second window for tokens minted in the reset's second.
- Follow-ups (not decided here): HMAC-peppered code hash, a dedicated reset rate limiter, purge
  of old `password_reset_codes` rows.

## Revisit When
Users get a "change password" endpoint (it must set `password_changed_at` too), or tokens move
to a format with a version claim.

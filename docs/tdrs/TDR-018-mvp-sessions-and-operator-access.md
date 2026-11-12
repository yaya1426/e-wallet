# TDR-018 — MVP Sessions, Password Reset, Client IP and Operator Access

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-10, MVP hardening). §4 amended by [TDR-021](TDR-021-password-reset-hardening.md) (2026-11-12): a reset also ends access tokens; disabled users cannot reset |
| Date       | 2026-11-10 |
| Deciders   | Yahya Elaraby |
| Related    | Extends TDR-011 (lifts "not in phase 1: refresh tokens, password reset, sessions revocation"); TDR-009 §3; TDR-013; TDR-017 |

## Context
Phase 1 (TDR-011) gave users a 15-minute access token and nothing else: a user had to log in
again every 15 minutes, could not log out, and could not recover a forgotten password. On
DigitalOcean every request reaches the wallet from the platform's proxy, so the per-IP login
limit was shared by all users. Operators could see problems only with SQL. These block an MVP.

## Decision
### 1. Client IP
New env `CLIENT_IP_HEADER` (default empty). When set, the client IP is the first address in that
request header if it parses as an IP; otherwise the host part of the connection address. On
DigitalOcean it is `do-connecting-ip`, set by the platform edge. A middleware resolves it once per
request; the login and registration limits and the access log use it.

### 2. Registration limit
`POST /v1/auth/register`: at most **5 attempts per client IP per hour** (`REGISTER_LIMIT_PER_HOUR`, default 5; raised only in local compose for the e2e suite; in memory, per instance,
fixed window, like the login limit) → `429 TOO_MANY_REQUESTS` (TDR-017).

### 3. Refresh tokens and logout
- Login also returns an opaque **refresh token**: 32 random bytes, base64url, stored only as its
  SHA-256 in `refresh_tokens` (owned by `identity`), valid **30 days** (`REFRESH_TOKEN_TTL=720h`).
- `POST /v1/auth/refresh {refresh_token}` returns a new access token **and a new refresh token**
  (rotation); the old one is marked used. Presenting a used or revoked token revokes the whole
  session (token family) — reuse detection. Unknown, expired, used or revoked → `401
  INVALID_REFRESH_TOKEN`; a disabled user → `403 USER_DISABLED`.
- `POST /v1/auth/logout {refresh_token}` (access token required) revokes that session; `204`, also
  when the token is unknown (idempotent).
- The access token is unchanged (HS256, 15 min).

### 4. Operator-assisted password reset
No SMS/email provider exists. An operator issues a **one-time 6-digit code** for a phone through the
internal API; it is valid **15 minutes**, stored hashed in `password_reset_codes` (owned by
`identity`), at most **5 wrong attempts**; issuing a new code invalidates older ones. The user calls
`POST /v1/auth/password-reset {phone, code, new_password}`; success sets the new password and
revokes all the user's refresh tokens. Any failure (unknown phone, wrong/expired/used code) is the
same `401 INVALID_RESET_CODE`. SMS delivery can replace the operator later without API changes.

### 5. Operator access
`/internal/v1/*` requires header `X-Internal-Api-Key` equal to env `INTERNAL_API_KEY` (≥ 32 bytes,
constant-time compare) → otherwise `401 UNAUTHENTICATED`. When the key is not configured, the
internal routes are not mounted (404). Operator actions are logged. Endpoints: discrepancies list
and resolve, wallet freeze/unfreeze, password-reset codes, an ops health summary.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Longer access tokens (24 h) instead of refresh tokens | No logout; a stolen token works all day. |
| SMS OTP for password reset | Needs a paid provider account; can be added later behind the same API. |
| Always trust `X-Forwarded-For` | Spoofable when no proxy is in front. |

## Consequences
- + Usable sessions, logout, account recovery, per-user rate limits on DO, operator tooling.
- − Two new tables; operator holds a shared static key (roles/SSO later).
- − In-memory limits are per instance (fine for one instance; shared store when scaling out).

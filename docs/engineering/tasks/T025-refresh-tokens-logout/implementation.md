# Implementation — T025 Refresh tokens and logout

## Summary
- **Login** now returns a session: the access token plus an opaque refresh token. The token is 32 random bytes, base64url (43 characters); only its SHA-256 is stored in `refresh_tokens`, valid `REFRESH_TOKEN_TTL` (default 30 days).
- **`POST /v1/auth/refresh`** runs in one transaction with the row locked:
  - unknown or expired token → 401 `INVALID_REFRESH_TOKEN`;
  - token already used or revoked → **the whole session (token family) is revoked**, committed, then 401, and a WARN is logged;
  - user disabled → 403;
  - otherwise the token is marked used and a new token in the same family is issued, again valid for 30 days (sliding).
- **`POST /v1/auth/logout`** (access token required) revokes the session of the given refresh token, but only if it belongs to the caller. It always answers 204.
- **`RevokeAllSessions`** is ready for the password reset (T027).

## Files
`migrations/000012_refresh_tokens`; `internal/identity/refresh.go` (new), `service.go` (`Login` returns a `Session`, which embeds `AccessToken`; `Config.RefreshTTL` and `Config.Log`), `http.go` (routes, response, `INVALID_REFRESH_TOKEN`), `queries.sql` + sqlcgen (and a nullable-timestamptz override in the identity block of `sqlc.yaml`); `internal/platform/config` (`REFRESH_TOKEN_TTL`, validated in `ValidateServe`); `cmd/wallet/app.go`; `api/openapi/wallet.yaml` (login response, refresh, logout, register 429, catalogue); `configs/.env.example`; tests `cmd/wallet/auth_refresh_integration_test.go`, `config_test.go`.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestAuth_RefreshRotation` (both tokens, 43-character refresh token, 2 592 000 s, stored only by hash) | ✅ |
| AC-2 | `TestAuth_RefreshRotation` (new pair, the old one is single-use, the new access token works) | ✅ |
| AC-3 | `TestAuth_RefreshReuseRevokesFamily` (reuse → 401 and the newer token is revoked; another device's session survives) | ✅ |
| AC-4 | `TestAuth_RefreshExpiredUnknownDisabled` (unknown, malformed → 400; sliding expiry; expired → 401; disabled → 403) | ✅ |
| AC-5 | `TestAuth_Logout` | ✅ |
| AC-6 | `TestAuth_RefreshConcurrent` (exactly one 200 and one 401) | ✅ |
| AC-7 | `TestConfig_RefreshTokenTTL`, OpenAPI catalogue and endpoints test | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 lint issues. A fragile "stored in clear" check in my first test version failed on hashes containing a zero byte; it now compares raw bytes. |
| `make e2e` (compose) | ✅ |
| Live on compose | login 200 with both tokens (900 / 2 592 000); refresh 200; reuse of the old token → 401 `INVALID_REFRESH_TOKEN`; the newer token after the reuse → 401; logout 204; refresh after logout → 401 |

## Follow-ups
- A cleanup job for expired or revoked `refresh_tokens` rows. About 96 rows per active user per day at one refresh every 15 minutes; fine for the MVP.

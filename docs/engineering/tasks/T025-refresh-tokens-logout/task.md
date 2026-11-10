# T025 — Refresh tokens and logout

| Field        | Value |
|--------------|-------|
| ID           | T025 |
| Status       | Ready |
| Created      | 2026-11-10 |
| Branch       | `task/T025-refresh-tokens-logout` |
| Depends on   | T024 |
| PRDs         | PRD-001 US-7 |
| TDRs         | TDR-018 §3, TDR-011, TDR-013 |
| Docs changed | — (TDR-018 written with T024) |

## What
Users stay signed in for 30 days: login returns a refresh token; `POST /v1/auth/refresh` rotates it
and returns a new access token; `POST /v1/auth/logout` ends the session. A reused refresh token
revokes the whole session (theft detection).

## Why
TDR-018 §3; PRD-001 US-7 (decided 2026-11-10).

## How
### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000012_refresh_tokens` | Table below |
| `internal/identity/refresh.go`, `queries.sql` | Issue, rotate, revoke |
| `internal/identity/http.go` | Login response gains `refresh_token`, `refresh_expires_in`; new routes |
| `internal/platform/config` | `REFRESH_TOKEN_TTL` (default `720h`, > 0, whole seconds) |
| `api/openapi/wallet.yaml` | Login response, `/v1/auth/refresh`, `/v1/auth/logout`, `INVALID_REFRESH_TOKEN` |

### Data
```sql
refresh_tokens(id uuid PK, user_id uuid NOT NULL REFERENCES users(id),
  family_id uuid NOT NULL, token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  used_at timestamptz NULL, revoked_at timestamptz NULL)
INDEX (family_id), INDEX (user_id)
```

### API
- `POST /v1/auth/login` → `200 {access_token, token_type, expires_in, refresh_token, refresh_expires_in}`.
- `POST /v1/auth/refresh` body `{"refresh_token": "…"}` → `200` same shape (new pair).
  Errors: body → `400 MALFORMED_REQUEST`; unknown/expired/used/revoked → `401 INVALID_REFRESH_TOKEN`;
  user disabled → `403 USER_DISABLED`.
- `POST /v1/auth/logout` (Bearer access token) body `{"refresh_token": "…"}` → `204`; unknown token,
  or a token of another user → still `204`, nothing revoked for others. No token → `401 UNAUTHENTICATED`.

### Behaviour
- Token: 32 bytes from `crypto/rand`, base64url without padding (43 chars); store `SHA-256(token)`.
- Login: new family (`family_id` = new UUIDv7), insert row (`expires_at = now + TTL`).
- Refresh, one DB tx: look up by hash `FOR UPDATE`:
  - not found / expired → 401;
  - `used_at` or `revoked_at` set → **revoke the family** (`revoked_at = now` on all its rows not yet
    revoked), commit, 401 (reuse detection; logged at WARN with `user_id`, `family_id`);
  - user disabled → 403 (token unchanged);
  - else mark `used_at = now`, insert the next token in the same family with `expires_at = now + TTL`
    (sliding 30 days), issue an access token → 200.
- Logout: find by hash; if it belongs to the caller, revoke its family; 204 always.
- `identity.RevokeAllSessions(ctx, tx, userID)` (used by T027).
- Expired rows are not deleted in this task (follow-up: cleanup job).

### Out of scope
Listing sessions, device names, cleanup of old rows.

## Acceptance Criteria
- [ ] AC-1 — Login returns both tokens; `refresh_expires_in` = 2 592 000; the DB stores only the hash.
- [ ] AC-2 — Refresh with a valid token → new access + new refresh token; the old refresh token no longer works; the new access token works on `/v1/me/wallet`.
- [ ] AC-3 — Reusing an already-used refresh token → 401 and the newer token of the same session is revoked too; another session (second login) is unaffected.
- [ ] AC-4 — Expired (fake clock past 30 days) or unknown token → 401 `INVALID_REFRESH_TOKEN`; disabled user → 403.
- [ ] AC-5 — Logout revokes the session (refresh fails afterwards); logout of an unknown token or another user's token → 204, nothing else revoked; no access token → 401.
- [ ] AC-6 — Two concurrent refreshes with the same token → exactly one 200; the other 401 (and the family is revoked).
- [ ] AC-7 — Config `REFRESH_TOKEN_TTL` default/validation; OpenAPI documents the endpoints and code.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-6 | integration HTTP (cmd/wallet fixture, fake clock) | `TestAuth_RefreshRotation`, `TestAuth_RefreshReuseRevokesFamily`, `TestAuth_RefreshExpiredUnknownDisabled`, `TestAuth_Logout`, `TestAuth_RefreshConcurrent` |
| AC-7 | unit | `TestConfig_RefreshTokenTTL`, OpenAPI catalogue test |

## Open Questions
| # | Question | Options | Status | Answer |
|---|----------|---------|--------|--------|
| 1 | Sessions design | refresh tokens / longer JWT | Answered | Refresh tokens, 30 days, rotation + reuse detection, logout |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Refresh tokens as TDR-018 §3 | Yahya | TDR-018 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |

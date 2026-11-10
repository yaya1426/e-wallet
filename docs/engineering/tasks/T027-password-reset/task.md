# T027 — Operator-assisted password reset

| Field        | Value |
|--------------|-------|
| ID           | T027 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T027-password-reset` |
| Depends on   | T025 (revoke sessions), T026 (internal key) |
| PRDs         | PRD-001 US-8 |
| TDRs         | TDR-018 §4–5 |
| Docs changed | — |

## What
An operator issues a one-time 6-digit code for a phone; the user sets a new password with it.
All the user's sessions are signed out.

## Why
TDR-018 §4; PRD-001 US-8 (decided 2026-11-10: operator-assisted, no SMS provider).

## How
### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000013_password_reset_codes` | Table below |
| `internal/identity/reset.go`, `queries.sql`, `http.go` | Issue code, reset password |
| `api/openapi/wallet.yaml` | Both endpoints, codes |

### Data
```sql
password_reset_codes(id uuid PK, user_id uuid NOT NULL REFERENCES users(id),
  code_hash bytea NOT NULL, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0, used_at timestamptz NULL, invalidated_at timestamptz NULL)
INDEX (user_id, created_at DESC)
```

### API
- `POST /internal/v1/users/{phone}/password-reset-code` (operator key; `{phone}` URL-escaped E.164)
  → `201 {phone, code, expires_at}`; `404 USER_NOT_FOUND`; invalid phone → `400 INVALID_PHONE`.
- `POST /v1/auth/password-reset` `{phone, code, new_password}` → `204`.
  Errors: body → 400 `MALFORMED_REQUEST`; password rule → `400 INVALID_PASSWORD`; anything about the
  phone/code (unknown phone, no active code, wrong, expired, used, too many attempts) → `401
  INVALID_RESET_CODE`; rate limit (shared login limiter: per phone + per IP) → `429 TOO_MANY_REQUESTS`.

### Behaviour
- Code: 6 digits from `crypto/rand` (000000–999999); `code_hash = SHA-256(user_id ‖ ":" ‖ code)`;
  valid 15 min. Issuing sets `invalidated_at` on the user's earlier unused codes (one tx).
- Reset, one tx: user by phone (unknown → 401); latest code that is unused, not invalidated, not
  expired, `FOR UPDATE` (none → 401); compare hash constant-time; wrong → `attempts+1`, and at 5 set
  `invalidated_at`; commit, 401. Right → bcrypt new password, `used_at = now`, `RevokeAllSessions`, 204.
- The operator action and the reset are logged (never the code or password).
- Login with the old password fails afterwards; the new one works.

### Out of scope
SMS/email delivery, self-service request endpoint.

## Acceptance Criteria
- [x] AC-1 — Operator issues a code → 201 with 6 digits and expiry ≈ now+15 min; DB holds only the hash; issuing again invalidates the first code.
- [x] AC-2 — Reset with the right code → 204; old password fails, new works; all refresh tokens of the user revoked; the code cannot be used twice.
- [x] AC-3 — Wrong code ×5 → the code is dead (even the right code then → 401); expired code (fake clock 16 min) → 401; unknown phone → 401 (same body).
- [x] AC-4 — Invalid new password → 400 `INVALID_PASSWORD` and the attempt is not counted.
- [x] AC-5 — Internal endpoint needs the key; unknown phone → 404; rate limit → 429.
- [x] AC-6 — OpenAPI documents both endpoints and codes.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-5 | integration HTTP (cmd/wallet, fake clock) | `TestPasswordReset_*` |
| AC-6 | unit | OpenAPI catalogue |

## Open Questions
| # | Question | Options | Status | Answer |
|---|----------|---------|--------|--------|
| 1 | Reset channel | operator-assisted / SMS / defer | Answered | Operator-assisted |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Operator-assisted reset per TDR-018 §4 | Yahya | TDR-018 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | Approved | Implemented and reviewed (no findings) |
| 2026-11-10 | Done | Finalized — PR opened |

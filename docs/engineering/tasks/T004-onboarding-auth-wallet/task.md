# T004 — Onboarding: register, login, my wallet

| Field        | Value |
|--------------|-------|
| ID           | T004 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T004-onboarding-auth-wallet` |
| Depends on   | T003 |
| PRDs         | PRD-001 US-1, US-2, US-3, BR-5, BR-8, BR-9, NFR-8 |
| TDRs         | TDR-011, TDR-013, TDR-017, TDR-012, TDR-010 |
| Docs changed | new TDR-017 (status codes 413/429 + error-code catalogue location); `docs/README.md` |

## What
A user can register with phone + password (which creates their EGP wallet with balance 0 in the
same DB transaction), log in to get a JWT, and read their wallet.

Deliverables:
- `internal/identity`: `users` table, registration, login, password hashing, JWT issue/verify,
  auth middleware, login rate limiter, `GetUser`, `GetUserByPhone`.
- `internal/wallet`: `wallets` table, `Open(ctx, tx, userID)`, `GetByUserID`, `View`, `GET /v1/me/wallet`.
- `internal/platform/httpserver`: strict JSON decoding helper and the request-user context helpers.
- `api/openapi/wallet.yaml` (OpenAPI 3.1) with the endpoints of this task and the shared
  error schema. Later tasks add to it.
- Config: `JWT_SECRET`, `JWT_TTL`.

## Why
- PRD-001 US-1 (register → wallet with balance 0), US-2 (login token), US-3 (see wallet), BR-5
  (exactly one wallet per user, created at registration).
- TDR-011: phone + password, bcrypt cost 12, HS256 JWT 15 min with `sub/exp/iat/jti`,
  `golang-jwt/jwt/v5`, middleware, login rate limit.
- TDR-013: error body, strict decoding (unknown fields rejected, 64 KB max), RFC 3339 UTC,
  `snake_case`. TDR-017: 413 and 429.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000003_identity_wallet.up/down.sql` | `users`, `wallets` |
| `internal/identity/model.go` | `User`, `Status` (`ACTIVE`/`DISABLED`), validation of phone/password/name, errors |
| `internal/identity/service.go` | `Register`, `Login`, `GetUser`, `GetUserByPhone`, `Authenticate` (named `VerifyToken` when planned; it verifies the token and the user's status — review T004 #7) |
| `internal/identity/token.go` | JWT issue/verify (HS256 only; any other `alg` is rejected) |
| `internal/identity/ratelimit.go` | In-memory fixed-window limiter (per phone, per IP), injected clock, mutex, old windows dropped |
| `internal/identity/http.go` | `POST /v1/auth/register`, `POST /v1/auth/login`, `RequireAuth` middleware |
| `internal/identity/queries.sql` + generated `internal/sqlcgen` | users queries |
| `internal/wallet/model.go`, `service.go`, `repository.go`, `queries.sql`, `http.go` | Wallet module, `GET /v1/me/wallet` |
| `internal/platform/httpserver/decode.go` | `DecodeJSON(w, r, dst) error`: `http.MaxBytesReader` 64 KB, `DisallowUnknownFields`, exactly one JSON value, typed errors → `400 MALFORMED_REQUEST` / `413 REQUEST_TOO_LARGE` |
| `internal/platform/httpserver/auth.go` | `WithUserID(ctx, uuid)`, `UserID(ctx) (uuid.UUID, bool)` (the context key lives in platform so every module can read it) |
| `internal/platform/config` | `JWTSecret` (`JWT_SECRET`, required, ≥ 32 bytes), `JWTTTL` (`JWT_TTL`, default `15m`, > 0) |
| `cmd/wallet/app.go` | Wire identity + wallet + ledger services; mount routes |
| `api/openapi/wallet.yaml` | New file |
| `configs/.env.example`, `docker-compose.yml` | Dev `JWT_SECRET` (clearly marked dev-only) |
| `.golangci.yml` | depguard (architecture §3 arrows): `identity` may import only `wallet` + platform; `wallet` may import only `ledger` + platform |
| `go.mod` | `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto` (bcrypt) |

### Data (migrations)
```sql
users(id uuid PK, phone_e164 text UNIQUE NOT NULL, full_name text NOT NULL,
      password_hash text NOT NULL, status text NOT NULL DEFAULT 'ACTIVE'
      CHECK (status IN ('ACTIVE','DISABLED')), created_at timestamptz NOT NULL)
wallets(id uuid PK, user_id uuid UNIQUE NOT NULL REFERENCES users(id),
        ledger_account_id uuid UNIQUE NOT NULL REFERENCES ledger_accounts(id),
        currency char(3) NOT NULL DEFAULT 'EGP' CHECK (currency='EGP'),
        status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','FROZEN')),
        created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)
```

### API
**`POST /v1/auth/register`** `{ "phone": "+201001234567", "full_name": "Ahmed Mohamed Ali", "password": "…" }`
→ `201 { "user_id", "phone", "full_name", "wallet_id", "created_at" }` (no token).

| Error | HTTP | Code |
|-------|------|------|
| Body malformed / unknown field / wrong type | 400 | `MALFORMED_REQUEST` |
| Body > 64 KB | 413 | `REQUEST_TOO_LARGE` |
| Phone not `^\+201[0125][0-9]{8}$` | 400 | `INVALID_PHONE` |
| Password < 8 or > 72 bytes | 400 | `INVALID_PASSWORD` |
| Name after trim < 2 or > 100 characters (runes) | 400 | `INVALID_NAME` |
| Phone already registered | 409 | `PHONE_ALREADY_REGISTERED` |

Validation order: decode → phone → password → name → uniqueness.

**`POST /v1/auth/login`** `{ "phone", "password" }` → `200 { "access_token", "token_type": "Bearer", "expires_in": 900 }`
(`expires_in` = `JWT_TTL` in seconds).

| Error | HTTP | Code |
|-------|------|------|
| Malformed | 400 | `MALFORMED_REQUEST` |
| Unknown phone or wrong password (same answer, same timing: bcrypt compare against a fixed dummy hash for unknown phones) | 401 | `INVALID_CREDENTIALS` |
| User `DISABLED` (only after a correct password) | 403 | `USER_DISABLED` |
| > 10 attempts / minute for this phone, or > 30 / minute from this IP | 429 | `TOO_MANY_REQUESTS` |

Rate limiting counts **every** login attempt (successful or not), with a fixed 1-minute window per key.
The client IP is `r.RemoteAddr` (host part). `X-Forwarded-For` is ignored in phase 1.

**Auth middleware** (`RequireAuth`): `Authorization: Bearer <jwt>`. Missing, malformed, bad signature,
wrong `alg`, or expired → `401 UNAUTHENTICATED`. Valid → user ID in context. A user who is now
`DISABLED` → `403 USER_DISABLED` (one PK lookup per request; acceptable at this scale).

**`GET /v1/me/wallet`** (auth) → `200 { "wallet_id", "currency": "EGP", "available_balance_minor": 0, "status": "ACTIVE" }`.
The balance is the cached `ledger_accounts.balance` (TDR-004 §5, BR-9).

### Behaviour and design notes
- **Register**, one `InTx`: insert user → `wallet.Open(tx, userID)` = generate wallet ID →
  `ledger.CreateUserAccount(tx, walletID)` → insert the wallet with that account ID. A unique
  violation on `phone_e164` → `PHONE_ALREADY_REGISTERED`. bcrypt hashing (cost 12) happens
  **before** `InTx` (so it is not repeated on a retry, and no slow CPU work runs inside the tx).
- JWT claims: `sub` = user ID, `iat`, `exp` = iat + TTL, `jti` = UUIDv7. Times come from the injected clock.
- Passwords and tokens are never logged. The request log must not include bodies (T001 already does not).
- Import directions: `identity → wallet → ledger` (architecture §3). `identity` does not call `ledger` directly.
- HTTP handlers call services only. Services return typed errors, and the handler maps them to codes.
  Error responses use `httpserver.WriteError`.

### Out of scope
Refresh tokens, logout/revocation, OTP, password reset, operator auth (`/internal/v1`, phase 2),
`X-Forwarded-For`, freezing a wallet through the API (operators use SQL in phase 1, PRD-001 BR-8).

## Acceptance Criteria
- [x] AC-1 — Given a valid register body, then `201` with the fields above. In the DB: one user (bcrypt hash, not the password), one `ACTIVE` EGP wallet, one `USER_WALLET:<wallet_id>` ledger account with balance 0, all committed together.
- [x] AC-2 — Given a failure after the user insert (forced in a test, e.g. ledger insert fails), then nothing is committed.
- [x] AC-3 — Given each invalid input in the register table, then the listed status + code with the TDR-013 error body. A registered phone → `409 PHONE_ALREADY_REGISTERED`. Two parallel registrations with the same phone → exactly one `201`.
- [x] AC-4 — Given correct credentials, then `200` with a JWT whose `sub` = user ID and `exp − iat` = `JWT_TTL`. Wrong password and unknown phone both → `401 INVALID_CREDENTIALS`. A disabled user with the correct password → `403 USER_DISABLED`.
- [x] AC-5 — Given 11 login attempts for one phone within a minute, then the 11th → `429 TOO_MANY_REQUESTS`. After the window passes (fake clock), attempts are allowed again. The per-IP limit of 30 works the same way.
- [x] AC-6 — Given no token, a garbage token, a token signed with another secret, an `alg: none` token, or an expired token (fake clock), then `GET /v1/me/wallet` → `401 UNAUTHENTICATED`.
- [x] AC-7 — Given a valid token, then `GET /v1/me/wallet` returns that user's wallet with `available_balance_minor` = the ledger balance (0 at first, and the posted amount after a test posting).
- [x] AC-8 — Unknown JSON fields, a JSON array body, trailing data, or a string for `phone`'s type mismatch → `400 MALFORMED_REQUEST`. A 65 KB body → `413 REQUEST_TOO_LARGE`.
- [x] AC-9 — `JWT_SECRET` missing or shorter than 32 bytes, or `JWT_TTL` ≤ 0 → `wallet serve` exits 1 with an error naming the variable.
- [x] AC-10 — `api/openapi/wallet.yaml` is valid OpenAPI 3.1 (checked in CI with a linter, e.g. `redocly lint`, pinned version via `npx`, or a Go validator; implementer picks one and records it) and describes these 3 endpoints + the error schema.
- [x] AC-11 — Ledger integrity (`ledgertest.AssertIntegrity`) holds after the integration tests.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2 | integration | `TestRegister_CreatesUserWalletAccount`, `TestRegister_RollsBackOnFailure` |
| AC-3 | unit + integration | `TestValidatePhone/Password/Name` (tables); `TestRegister_DuplicatePhone`, `TestRegister_ParallelSamePhone` |
| AC-4 | integration (HTTP via `httptest`) | `TestLogin_Success`, `TestLogin_InvalidCredentials`, `TestLogin_DisabledUser` |
| AC-5 | unit | `TestRateLimiter_PerPhone`, `TestRateLimiter_PerIP`, `TestRateLimiter_WindowReset` |
| AC-6 | unit | `TestRequireAuth` (table) |
| AC-7 | integration HTTP | `TestGetMyWallet` |
| AC-8 | unit | `TestDecodeJSON` (table) |
| AC-9 | unit | `TestConfig_Load` extended |
| AC-10 | CI | OpenAPI lint step |
| AC-11 | integration | `ledgertest.AssertIntegrity` in the test cleanup |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Phone, password, name rules, login rate limits | strict defaults / also local phone format | Answered | Strict defaults |
| 2 | Response shapes, error codes, JWT config | defaults / register returns token | Answered | Defaults (no token on register) |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Phone `+20` + `1[0125]` + 8 digits only; password 8–72 bytes; name 2–100; login 10/min per phone, 30/min per IP → 429 | Yahya | TDR-017, this task |
| 2026-11-05 | Register 201 without a token; login `{access_token, token_type, expires_in}`; codes `PHONE_ALREADY_REGISTERED`, `INVALID_CREDENTIALS`, `USER_DISABLED`, `UNAUTHENTICATED`, `MALFORMED_REQUEST`, `REQUEST_TOO_LARGE`; `JWT_SECRET` ≥ 32 bytes, `JWT_TTL` default 15m | Yahya | TDR-017, this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-06 | In Progress | implement-task started on `task/T004-onboarding-auth-wallet`, stacked on the unmerged T003 branch (PR #5, auto-merge) per the user's loop decision |
| 2026-11-06 | In Review | Implemented and verified: make ci ok (lint 0), 228 tests pass, compose + curl + e2e pass. See implementation.md |
| 2026-11-06 | Changes Requested | Review round 1: 1 Blocker (login limiter), 5 Minor, 2 Nit |
| 2026-11-06 | Approved | Review round 2: all fixed except #6 (register rate limit → user decision); make ci ok, 237 tests |
| 2026-11-06 | Approved | PR opened without auto-merge: production needs a JWT_SECRET on DigitalOcean first (writing it was blocked for the agent; the user sets it), then the PR can merge |
| 2026-11-09 | Done | JWT_SECRET added on DigitalOcean by the user (recommended: mark it Encrypted, scope Run time); auto-merge enabled on PR #6 |

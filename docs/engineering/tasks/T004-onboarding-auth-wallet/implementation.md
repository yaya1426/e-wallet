# T004 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-06 |
| Branch | `task/T004-onboarding-auth-wallet`, stacked on `task/T003-ledger-core` (PR #5, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **Migration `000003_identity_wallet`**: `users` (unique `phone_e164`, status `ACTIVE`/`DISABLED`) and `wallets` (unique `user_id`, unique `ledger_account_id` → `ledger_accounts`, EGP only, status `ACTIVE`/`FROZEN`).
- **`internal/identity`**:
  - Validation in the API order (phone `^\+201[0125][0-9]{8}$` → password 8–72 bytes → name 2–100 runes after trim).
  - `Register`: bcrypt cost 12 before the transaction, then one `InTx` with user + `WalletOpener.Open`.
  - `Login`: an unknown phone is compared against a dummy hash; disabled is only revealed after a correct password.
  - `Authenticate`: HS256 only; `exp`/`iat` required; `jti` is a UUIDv7.
  - Plus `GetUser`, `GetUserByPhone`, the in-memory fixed-window `LoginLimiter` (10/phone and 30/IP per minute; every attempt counts; ended windows pruned) and `RequireAuth`.
  - HTTP: `POST /v1/auth/register`, `POST /v1/auth/login`.
- **`internal/wallet`**: `Open(ctx, tx, userID)` creates the `USER_WALLET:<wallet_id>` ledger account and the wallet row in the caller's transaction; `GetByUserID`; `View` (cached ledger balance); `GET /v1/me/wallet`.
- **`internal/platform/httpserver`**: `DecodeJSON` + `WriteDecodeError` (64 KB `MaxBytesReader`, unknown fields rejected, exactly one JSON value → `400 MALFORMED_REQUEST` / `413 REQUEST_TOO_LARGE`); `WithUserID`/`UserID`; `CodeTryAgain`.
- **Config**: `JWT_SECRET` (≥ 32 bytes) and `JWT_TTL` (default 15m, > 0), checked by `Wallet.ValidateServe()` only for `serve` (see Deviations #1).
- **Wiring**: `cmd/wallet/app.go` `newAPI` builds ledger → wallet → identity and mounts the routes.
- **`api/openapi/wallet.yaml`** (OpenAPI 3.1): the 3 endpoints, bearer auth, the shared `Error` schema and every error code. It is checked by `api/openapi/openapi_test.go`.
- **depguard**: `identity-deps` (no business imports) and `wallet-deps` (`ledger` only), for non-test files.
- **Dev config**: `docker-compose.yml` and `configs/.env.example` get a clearly marked dev-only `JWT_SECRET`.
- **Dependencies**: `golang-jwt/jwt/v5 v5.3.1`, `golang.org/x/crypto v0.58.0`, `pb33f/libopenapi-validator v0.16.1` (+ `libopenapi v0.41.3`), test only.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestRegister_CreatesUserWalletAccount` (bcrypt hash, ACTIVE EGP wallet, `USER_WALLET:<id>` balance 0), `TestAPI_RegisterLoginWallet` | ✅ |
| AC-2 | `TestRegister_RollsBackOnFailure` (failure after user + wallet + ledger account inserts → none committed) | ✅ |
| AC-3 | `TestValidatePhone/Password/Name`, `TestValidate_OrderPhonePasswordName`, `TestAPI_RegisterErrors` (every code + body shape), `TestRegister_DuplicatePhone`, `TestRegister_ParallelSamePhone` (10 parallel → 1 ok, 9 taken) | ✅ |
| AC-4 | `TestAPI_RegisterLoginWallet` (`expires_in` 900), `TestTokens_IssueAndVerify` (`sub`, `exp − iat` = TTL, jti v7), `TestAPI_LoginErrors`, `TestLogin_Service` | ✅ |
| AC-5 | `TestRateLimiter_PerPhone`, `TestRateLimiter_PerIP`, `TestRateLimiter_WindowReset`, `TestRateLimiter_PrunesEndedWindows`, `TestAPI_LoginRateLimit` | ✅ |
| AC-6 | `TestRequireAuth` (no header, basic, empty bearer, garbage, other secret, `alg: none`, expired), `TestTokens_VerifyRejects` (+ HS512, no exp, bad sub), `TestAPI_WalletRequiresAuth` | ✅ |
| AC-7 | `TestAPI_RegisterLoginWallet` (0), `TestAPI_WalletShowsLedgerBalance` (15075 after a posting) | ✅ |
| AC-8 | `TestDecodeJSON` (unknown field, array, trailing data/garbage, wrong types incl. decimal for int64, empty, invalid, 65 KB → 413), `TestDecodeJSON_ExactlyAtLimitIsAccepted` | ✅ |
| AC-9 | `TestConfig_ValidateServe_JWT`, `TestServe_InvalidConfig_Exit1NamesVariable` (no secret, 31 bytes, zero TTL) | ✅ |
| AC-10 | `TestSpec_IsValidOpenAPI31` (libopenapi-validator; a deliberately broken spec makes it fail), `TestSpec_DescribesTheEndpoints`, `TestSpec_CataloguesEveryErrorCode`. Runs in CI's `test` job | ✅ |
| AC-11 | `ledgertest.AssertIntegrity` in the cleanup of the identity and `cmd/wallet` API integration tests; live SQL check on the stack | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, fmt, vet, lint, sqlc-check, build, unit, integration) | ✅ exit 0; golangci-lint 0 issues |
| Tests (`-race`, unit + integration) | ✅ 228 passed, 0 failed, 0 skipped |
| Coverage | httpserver 91.9%, config 89.1%, cmd/wallet 85.7%, identity 72.6%, wallet 70.2% (measured with `-coverpkg` from `cmd/wallet`, since its tests live there) |
| depguard probes | `identity → wallet` and `wallet → identity` imports are each reported; probes removed |
| `POSTGRES_HOST_PORT=5433 make up` + `make e2e` (incl. new `TestSmoke_RegisterLoginWallet`) | ✅ |
| Ledger integrity on the stack | debits = credits = 0, 0 drifts, users = wallets = user accounts = 3 |

### HTTP checks (compose stack, curl)
| Request | Expected | Actual |
|---------|----------|--------|
| register valid | 201 + ids | ✅ 201 |
| register same phone | 409 `PHONE_ALREADY_REGISTERED` | ✅ |
| local phone / 1-byte password / blank name | 400 `INVALID_PHONE` / `INVALID_PASSWORD` / `INVALID_NAME` | ✅ |
| unknown field / 65 KB body | 400 `MALFORMED_REQUEST` / 413 `REQUEST_TOO_LARGE` | ✅ |
| login wrong password / unknown phone | 401 `INVALID_CREDENTIALS` (same body) | ✅ |
| login ok | 200, `Bearer`, `expires_in` 900 | ✅ |
| `GET /v1/me/wallet` with token / none / garbage | 200 balance 0 ACTIVE / 401 / 401 `UNAUTHENTICATED` | ✅ |
| 12th login attempt for one phone | 429 `TOO_MANY_REQUESTS` | ✅ |
| user set `DISABLED`: login / wallet with old token | 403 `USER_DISABLED` / 403 | ✅ |
| password in container logs | none | ✅ 0 matches |

## Review fixes (round 1 → 2, 2026-11-06)
- **Login limiter:** checks the IP first (a blocked IP no longer counts any phone) and counts only well-formed phones (no unbounded memory from attacker strings).
- **Login input:** a malformed phone or a > 72-byte password is rejected after the dummy bcrypt compare.
- **Strict decoding:** `DecodeJSON` requires a JSON object (rejects `null`/scalars). Keys match case-insensitively (encoding/json), as documented.
- **Response data:** `created_at` is truncated to microseconds. `JWT_TTL` must be whole seconds.
- **OpenAPI:** documents `500 INTERNAL_ERROR` everywhere.
- **Re-verification:** `make ci` exit 0 (lint 0 issues); 237 tests pass; compose + e2e ok; live `null` body → 400, 73-byte password → 401.

## Deviations from the plan
1. **JWT settings are checked only by `serve`** (`config.Wallet.ValidateServe`), not at config load. The DigitalOcean pre-deploy job runs `wallet migrate up` with only `DATABASE_URL`; a load-time requirement would break every deploy. This matches the AC ("`wallet serve` exits 1").
2. **OpenAPI check uses a Go validator** (`pb33f/libopenapi-validator v0.16.1`, ≥ 2 weeks old), run as a test in CI's `test` job. Running `npx @redocly/cli` was blocked in this environment (it downloads and executes external code). The task allows either.
3. **`identity` does not import `wallet`:** it depends on a consumer-defined `WalletOpener` interface, and `main` passes `*wallet.Service`. This is stricter than the planned `identity → wallet` arrow.
4. **`identity.Config.BcryptCost`:** 0 means the TDR-011 cost 12; only tests pass `bcrypt.MinCost`.
5. **`database.ErrTryAgain` → `503 TRY_AGAIN`** in identity's error mapping (TDR-013 lists it; the task's tables didn't). Catalogued in the OpenAPI file.
6. **A missing wallet for an authenticated user → 500** (logged). It can't happen, since BR-5 creates both in one transaction; there's no client error code for it.
7. **`TestMigration_LedgerSchemaAndSeed`** now asserts version ≥ 2 instead of = 2, because every new migration bumps the version. Two migration-status tests now expect 3.
8. **sqlc** writes models for **all** tables into every module's Go-internal `models.go` (the schema is all migrations), so `ledger/internal/sqlcgen/models.go` changed. It stays private to each module.

## Follow-ups (not done)
- **Register has no rate limit** (review #6): each well-formed request costs a cost-12 bcrypt, and the 409 lets anyone enumerate registered phones. TDR-011 limits only login, so adding a limit is a decision for the user.
- **Production `JWT_SECRET` on DigitalOcean**: the web service needs a `JWT_SECRET` secret (≥ 32 bytes) before this deploys, or `serve` exits 1 and the deploy rolls back. To be set at finalize.
- **Per-IP login limit behind a load balancer**: on DigitalOcean every request's `RemoteAddr` is the platform's proxy, so the 30/min per-IP limit is effectively shared by all users. Phase 1 ignores `X-Forwarded-For` by decision. Needs a decision: trust the platform's `X-Forwarded-For` / `DO-Connecting-IP`, or drop the per-IP limit there.
- depguard ignores blank (`_`) imports. That's harmless today.

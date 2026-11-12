# T029 — Implementation

## Summary
A password reset now ends access tokens as well as refresh tokens, disabled users cannot get
or use a reset code, and the reset log lines carry the user id (TDR-021).

| Area | Change |
|------|--------|
| `migrations/000014_users_password_changed_at` | `users.password_changed_at timestamptz NULL` (down: drop column, local only) |
| `internal/identity/queries.sql` + sqlcgen | `UpdatePasswordHash` sets `password_changed_at`; `SELECT *` users queries return it (sqlc regenerated every module's `models.go`, only the `User` struct changed) |
| `internal/identity/token.go` | `verify` also returns `iat`; a token without `iat` is rejected |
| `internal/identity/service.go` | `Authenticate` rejects a token issued before `password_changed_at` (whole seconds) → `ErrUnauthenticated`; helper `issuedBeforePasswordChange` |
| `internal/identity/model.go`, `repository.go` | `User.PasswordChangedAt` |
| `internal/identity/reset.go` | Disabled user: issue → `ErrUserDisabled` (403), reset → `ErrInvalidResetCode` with nothing counted; `user_id` in the issue, reset and wrong-code log lines |
| `api/openapi/wallet.yaml` | 403 `USER_DISABLED` on the reset-code endpoint; reset and `UNAUTHENTICATED` descriptions |
| Docs | TDR-021 (new), TDR-018 status line, `docs/README.md`, `data-model.md` |

## AC → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestPasswordReset_RevokesAccessTokens` (cmd/wallet), `TestIssuedBeforePasswordChange` (unit) | ✅ |
| AC-2 | `TestPasswordReset_RevokesAccessTokens` (other user, NULL column), `TestIssuedBeforePasswordChange` "never changed" | ✅ |
| AC-3 | `TestPasswordReset_DisabledUser` (cmd/wallet), `TestPasswordReset_DisabledUserRefused` (identity) | ✅ |
| AC-4 | `TestPasswordReset_ConcurrentUse` | ✅ |
| AC-5 | `TestPasswordReset_ConcurrentWrongGuesses` (8 parallel wrong guesses → attempts = 5, invalidated, right code → 401) | ✅ |
| AC-6 | `TestPasswordReset_UniformFailures` (unknown phone, wrong, expired, disabled: same status and body minus `request_id`) | ✅ |
| AC-7 | `TestPasswordReset_LogsUserIDNoSecrets` (JSON handler capture) | ✅ |
| AC-8 | `TestSpec_ResetCodeRefusesDisabledUser`, `TestSpec_CataloguesEveryErrorCode` | ✅ |

T027 tests (`TestPasswordReset_Flow`, `_Failures`, `_OperatorEndpoint`) unchanged and passing.

## Verification (2026-11-12, Linux container, Go 1.27.3, Docker 29.9.2)
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `go mod tidy -diff` | ✅ no diff |
| 2 | Format | `gofmt -l .` | ✅ empty |
| 3 | Vet | `go vet -tags 'integration e2e' ./...` | ✅ |
| 4 | Lint | `golangci-lint run ./...` (v2.15.0 built with go1.27.3) | ✅ 0 issues |
| — | sqlc | `sqlc generate` (v1.31.1) leaves generated code unchanged | ✅ |
| 5 | Build | `go build ./...` | ✅ |
| 6 | Unit | `go test -race -count=1 -cover ./...` | ✅ all packages ok |
| 7 | Integration | `go test -race -count=1 -tags integration ./...` | ✅ all packages ok (cmd/wallet 116 s) |
| 8 | Coverage | integration profile | `issuedBeforePasswordChange` 100 %, `verify` 100 %, `IssueResetCode` 80.8 %, `ResetPassword` 77.5 %, `Authenticate` 76.9 % (uncovered: DB error branches); identity pkg 53.8 %, cmd/wallet 84.5 % |
| 9 | Run | Postgres 18 (compose) + `wallet migrate up` (version 14) + `wallet serve` + `banksim` as binaries | ✅ `/readyz` 200 |
| 10 | HTTP | see below; plus `go test -tags e2e ./test/e2e/...` | ✅ all as expected; e2e ok (34.7 s) |
| 11 | Ledger | `ledgertest.AssertIntegrity` runs at the end of every integration test; `reconciliation_discrepancies` = 0 after e2e | ✅ |
| 12 | Tear down | `docker compose down -v` | ✅ |

`make up` (Docker image build) could not run in this container: `go mod download` inside the
build cannot verify TLS through the sandbox's egress proxy. Steps 9–10 used the same binaries
outside Docker instead. CI builds the images.

### HTTP checks
| Request | Expected | Actual |
|---------|----------|--------|
| `GET /v1/me/wallet` with token from login | 200 | 200 |
| Operator issues code → `POST /v1/auth/password-reset` | 204 | 204 |
| `GET /v1/me/wallet` with the pre-reset token | 401 `UNAUTHENTICATED` | 401 `UNAUTHENTICATED` |
| Login with new password, `GET /v1/me/wallet` | 200 | 200 |
| Issue code for a DISABLED user | 403 `USER_DISABLED` | 403 `USER_DISABLED` |
| Reset for a DISABLED user (valid code) | 401 `INVALID_RESET_CODE`, attempts 0, unused | 401, `attempts=0`, `used_at` NULL |
| Reset for unknown phone | same 401 body | same 401 body |
| Issue code without key | 401 | 401 |
| Logs | `user_id`, no phone/code | `{"msg":"password reset code issued","user_id":"01a1d08e-…"}` |

## Deviations
None from the plan. The `password reset: wrong code` log line was added to TDR-021 §3 and the
task's How before implementation.

## Follow-ups (not done)
- HMAC-peppered reset-code hash, dedicated reset rate limiter, purge of old `password_reset_codes` rows (TDR-021 follow-ups).
- `data-model.md` does not describe `refresh_tokens` and `password_reset_codes` yet (pre-existing gap from T025/T027).

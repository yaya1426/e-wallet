# Implementation — T027 Operator-assisted password reset

## Summary
- **Issuing a code:** `POST /internal/v1/users/{phone}/password-reset-code` (operator key) creates a 6-digit code from `crypto/rand`, valid 15 minutes. Only `SHA-256(user_id:code)` is stored in `password_reset_codes`. Issuing invalidates the user's earlier unused codes. Answers: `201 {phone, code, expires_at}`; `404 USER_NOT_FOUND`; `400 INVALID_PHONE`.
- **Resetting:** `POST /v1/auth/password-reset {phone, code, new_password}` shares the login rate limit (per phone and per client IP).
  - The new password is validated first; an invalid one gets `400 INVALID_PASSWORD` and the attempt isn't counted.
  - It then runs in one transaction: the user's latest active code is locked and compared in constant time.
  - A wrong code counts an attempt, which is committed; the fifth wrong attempt ends the code.
  - A right code is marked used, the bcrypt hash is updated, and `RevokeAllSessions` signs the user out everywhere → `204`.
  - Every other failure gets the same `401 INVALID_RESET_CODE`.
- **Logs** never contain the code, the phone or the password.

## Files
`migrations/000013_password_reset_codes`; `internal/identity/reset.go` (new), `http.go` (two endpoints, `InternalRoutes`, codes), `queries.sql` + sqlcgen; `cmd/wallet/app.go` (mounts the identity operator route); `api/openapi/wallet.yaml` (+ test); tests `cmd/wallet/password_reset_integration_test.go`.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestPasswordReset_Flow` (6 digits, expiry = now + 15 min, not stored in clear, a re-issue invalidates the first code) | ✅ |
| AC-2 | `TestPasswordReset_Flow` (204; old password 401; new password 200; refresh token revoked; single use) | ✅ |
| AC-3 | `TestPasswordReset_Failures` (5 wrong → even the right code then fails; expired after 16 min; unknown phone gets the same 401) | ✅ |
| AC-4 | `TestPasswordReset_Failures` (short password → 400, not counted) | ✅ |
| AC-5 | `TestPasswordReset_OperatorEndpoint` (401 without the key, 404 unknown phone, 400 invalid phone, 429 from the shared limit) | ✅ |
| AC-6 | OpenAPI path and catalogue tests | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 issues |
| `make e2e` | ✅ |
| Compose | register → operator code → reset 204 → login with the new password 200 |

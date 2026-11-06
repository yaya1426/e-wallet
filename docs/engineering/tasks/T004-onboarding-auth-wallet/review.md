# Review — T004

## Round 2 (2026-11-06)

Verdict: **Approved**

Summary: The Blocker, Minors #2–#5 and both Nits are fixed and tested. `make ci` passes (exit 0,
lint 0 issues), 237 tests pass with `-race`, and compose + e2e + live checks pass. #6 (register
rate limit) is deliberately not changed: it is a new rule, so it is raised with the user as a
follow-up.

| # | Round-1 severity | Status | Evidence |
|---|------------------|--------|----------|
| 1 | Blocker | **Fixed** | `LoginLimiter.Allow` checks the IP first and returns early, and counts only phones matching the regex (`ratelimit.go`). Tests: `TestLoginLimiter_BlockedIPDoesNotCountPhones` (an attacker IP over its limit no longer locks out a victim), `TestLoginLimiter_MalformedPhonesAreNotStored` (no counter for non-phones) |
| 2 | Minor | **Fixed** | `Login`: malformed phone or > 72-byte password → dummy compare + `ErrInvalidCredentials`. `TestLogin_PasswordOver72BytesRejected`; live 73-byte login → 401 |
| 3 | Minor | **Fixed** | `components/responses/Internal` on all three operations; `CodeInternal` in the catalogue test |
| 4 | Minor | **Fixed** | `CreatedAt` truncated to µs; `TestRegister_CreatedAtMatchesStoredValue` |
| 5 | Minor | **Fixed** | `DecodeJSON` requires a JSON object (`null`, string, number, whitespace → 400); case-insensitive keys documented; live `null` → 400 `MALFORMED_REQUEST` |
| 6 | Minor | **Open → user decision** | Recorded in `implementation.md` follow-ups |
| 7 | Nit | **Fixed** | Task table names `Authenticate` |
| 8 | Nit | **Fixed** | `ValidateServe` requires whole-second `JWT_TTL` (config test) |

New code reviewed against the checklist: no new findings.

## Round 1 (2026-11-06)

Verdict: **Changes Requested**

Verdict: **Changes Requested**

Summary: Registration, login, JWT and the wallet view match the spec and are well tested. gofmt
is clean, vet is ok, lint shows 0 issues, 228 tests pass, and the live checks pass. One
**Blocker** in the login rate limiter: it lets one client exhaust memory and lock out any user. I
also verified five Minor issues and two Nits. This round merges my review with an independent
reviewer's; every finding below was checked in the code.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | **Blocker** | `internal/identity/http.go:98`, `internal/identity/ratelimit.go:84-86` | Security (checklist severity table), C "no unbounded …" | `Allow(req.Phone, ip)` counts the **raw, unvalidated** phone, up to ~64 KB of attacker text, and counts it **even when the IP is already over its limit**. (a) Memory: unique long "phones" grow the map without bound inside a window (≈ 3.6 GB/min at 1k req/s × 60 KB). (b) Lockout: one blocked IP can still push any victim's phone counter over 10, locking real users out every minute. | Check the IP limit **first** and return 429 without touching the phone counter. Count a phone only if it matches the phone regex; a malformed phone gets `INVALID_CREDENTIALS` after the dummy compare. |
| 2 | Minor | `internal/identity/service.go:122` | Security / contract (8–72 bytes) | Login passes passwords over 72 bytes to `bcrypt.CompareHashAndPassword`, which ignores bytes past 72, so "72-byte password + any suffix" also logs in. | Over 72 bytes → dummy compare, then `ErrInvalidCredentials`. |
| 3 | Minor | `api/openapi/wallet.yaml`; `identity/http.go:153`, `wallet/http.go:41,49` | TDR-017 §3 (catalogue) | Handlers can answer `500 INTERNAL_ERROR`, but the spec lists no 500 and the catalogue test doesn't check that code. | Add a shared `Internal` response to every operation; add `CodeInternal` to the catalogue test. |
| 4 | Minor | `internal/identity/service.go:83` | API consistency | The 201 `created_at` has nanoseconds, while PostgreSQL stores microseconds, so later reads differ from the 201 body. | `s.clock.Now().Truncate(time.Microsecond)`. |
| 5 | Minor | `internal/platform/httpserver/decode.go:34-46` | TDR-013 strict decoding | A `null` body decodes into a struct without error, so register answers `INVALID_PHONE` (not `MALFORMED_REQUEST`) and login counts an attempt for phone `""`. Also, `encoding/json` matches keys case-insensitively and last-wins on duplicates. | Reject a body whose first JSON token isn't `{`. Document the case-insensitive matching in `DecodeJSON`'s comment, as a property of `encoding/json`. |
| 6 | Minor (follow-up, needs a decision) | `internal/identity/http.go` (register) | Beyond the spec | `/v1/auth/register` has no rate limit. Each well-formed request costs a cost-12 bcrypt (CPU), and the 409 lets anyone enumerate registered phones. TDR-011 only limits login. | Not changed here: a new limit is a product/security decision. Recorded as an open question for the user. |
| 7 | Nit | `task.md` file table | Plan conformance | The task names `VerifyToken`; the code's `Authenticate` (verify + user-status check) is clearer. | Update the task table. |
| 8 | Nit | `internal/identity/http.go:110` | API | `expires_in` drops any sub-second part of `JWT_TTL`. | `ValidateServe`: `JWT_TTL` must be whole seconds. |

### Checked and fine
- **JWT:**
  - Only HS256 is accepted (`none` and HS512 rejected and tested).
  - `exp` is required and `iat` is checked; the injected clock means no leeway.
  - A `sub` that isn't a UUID, or a user who no longer exists, gets 401.
  - `jti` is a UUIDv7, and `exp − iat` = TTL is tested.
  - The secret is never logged; config errors print only its length.
- **Passwords and login:**
  - bcrypt cost 12 runs before `InTx`.
  - Unknown phone, wrong password, and a disabled user with a wrong password each do one lookup plus one bcrypt and return the same 401.
  - `USER_DISABLED` only after a correct password; an existing token stops working once the user is disabled.
- **Registration atomicity:** user + wallet + ledger account in one `InTx`; IDs and timestamps are regenerated per attempt; duplicates map to 409, `ErrTryAgain` to 503. Rollback and parallel-same-phone are tested.
- **Boundaries:** `identity` imports no business module (`WalletOpener`), and `wallet` imports only `ledger` (`go list`); depguard enforces both (probed). The request-user context key lives in platform.
- **Data:** the migration matches the task (constraint names, FKs, CHECKs); sqlc per module.
- **API:** field names, status codes and error codes match the task tables and OpenAPI (except #3); the strict decoder covers every AC-8 case.
- **Tests:** every AC has tests at unit, integration and HTTP level; ledger integrity is asserted in the integration cleanups; e2e smoke covers the flow.

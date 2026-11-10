# Implementation — T024 Real client IP behind the proxy + registration rate limit

## Summary
- **Client IP:** `httpserver.NewHandler` takes `WithClientIPHeader(header)`. A middleware resolves the client IP once per request:
  - with the header configured, the first valid IP in that header;
  - otherwise, or when the header is missing or invalid, the connection host.

  It's stored in the request context and read with `httpserver.ClientIP(r)`. The access log adds `client_ip`, and the login limiter uses it.
- **Registration limit:** `identity.RegisterLimiter` allows `REGISTER_LIMIT_PER_HOUR` registration attempts per client IP per hour (default 5). Every attempt counts, checked after the body is decoded. Over the limit → `429 TOO_MANY_REQUESTS` "Too many registrations from this network. Try again later."
- **Config:** `CLIENT_IP_HEADER` (validated as a header name) and `REGISTER_LIMIT_PER_HOUR` (≥ 1).
- **DigitalOcean:** `CLIENT_IP_HEADER=do-connecting-ip` set with doctl. Applying the spec needs `apps update` without `spec validate`, because `validate` rejects the already-encrypted secrets.

## Deviation (to note)
The limit is configurable instead of a constant. The cmd/wallet test fixtures and the e2e suite register many users from one IP, so a fixed 5 per hour would make CI fail.
- The default stays 5, as decided, and DigitalOcean uses the default.
- Local compose sets 1000, marked DEV ONLY; the API test fixture also sets 1000.
- TDR-018 §2 records this.

## Files
`internal/platform/httpserver/clientip.go` (+ test), `handler.go`, `middleware.go`; `internal/identity/ratelimit.go`, `http.go` (`NewHandler` takes the register limiter; the old `clientIP` and its test moved to httpserver); `internal/platform/config`; `cmd/wallet/app.go`; `docker-compose.yml`; `configs/.env.example`; TDR-018 (new), TDR-011, PRD-001, `docs/README.md`; tests `cmd/wallet/ratelimit_integration_test.go`, the API fixture.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestClientIP` (9 cases: not configured, header, list, IPv6, invalid, absent, IPv6 connection, no port; no middleware) | ✅ |
| AC-2 | `TestLogin_PerClientIPBehindProxy` (30 failures from one IP → 429 on the 31st; another IP still gets 401) | ✅ |
| AC-3 | `TestRegister_RateLimited` (5 → 201, 6th → 429, another IP → 201, after one hour → 201) | ✅ |
| AC-4 | `TestAccessLog_ClientIP`; compose access log shows `client_ip` | ✅ |
| AC-5 | `TestConfig_ClientIPHeader`, config defaults test | ✅ |
| AC-6 | Env var set on DigitalOcean. The log check after deploy is recorded in T028. | ✅ set (verified in T028) |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 lint issues |
| `make e2e` on compose | ✅; access log lines carry `client_ip` |

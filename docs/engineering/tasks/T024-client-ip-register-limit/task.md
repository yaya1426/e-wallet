# T024 — Real client IP behind the proxy + registration rate limit

| Field        | Value |
|--------------|-------|
| ID           | T024 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T024-client-ip-register-limit` |
| Depends on   | T004 (login limiter) |
| PRDs         | PRD-001 §7 (auth), NFR (security) |
| TDRs         | TDR-018 §1–2, TDR-011, TDR-017 (429) |
| Docs changed | new TDR-018; TDR-011 status line; PRD-001 US-7..9; docs index |

## What
- The wallet knows the real client IP behind DigitalOcean's proxy (env `CLIENT_IP_HEADER`), so the
  per-IP login limit is per user again, not shared by everyone.
- `POST /v1/auth/register` is limited to 5 attempts per client IP per hour.
- The access log records `client_ip`.
- `CLIENT_IP_HEADER=do-connecting-ip` is set on the DigitalOcean app (doctl, approved earlier).

## Why
TDR-018 §1–2 (decided 2026-11-10). T004 implementation.md flagged the shared per-IP limit on DO.

## How
### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/platform/httpserver/clientip.go` | `ClientIPMiddleware(header)` puts the client IP in the request context; `ClientIP(ctx) string` |
| `internal/platform/httpserver/handler.go`, `middleware.go` | `NewHandler` takes the header (option) and installs the middleware before the access log; access log adds `client_ip` |
| `internal/identity/ratelimit.go` | `RegisterLimiter` (5 per IP per hour); `clientIP(r)` replaced by `httpserver.ClientIP(r.Context())` |
| `internal/identity/http.go` | Register checks the limiter first → `429 TOO_MANY_REQUESTS` "Too many registrations from this network. Try again later." |
| `internal/platform/config` | `CLIENT_IP_HEADER` (string, default empty; when set must be a valid header name: letters, digits, `-`) |
| `cmd/wallet/app.go` | Pass the header to `newHandler`; build `RegisterLimiter` |
| `api/openapi/wallet.yaml` | 429 on register |
| `configs/.env.example` | `CLIENT_IP_HEADER=` (empty locally) |

### Behaviour
- Resolution: header set and the request has it → take the text before the first `,`, trim spaces;
  if `net.ParseIP` accepts it → that; else fall back to the host of `RemoteAddr` (also when the
  header is absent). IPv6 addresses are kept as parsed text.
- Register limiter counts **every** attempt (valid or not) per IP; checked before decoding the body
  is fine either way — check **after** decoding so malformed bodies stay 400 (same order as login).
- Limits are in memory per instance (TDR-018 consequence).

### Out of scope
Distributed rate limiting; changing the login limits.

## Acceptance Criteria
- [x] AC-1 — `CLIENT_IP_HEADER` empty → client IP = connection host (current behaviour); set and present with `"203.0.113.7"` or `"203.0.113.7, 10.0.0.1"` → `203.0.113.7`; present but invalid (`"unknown"`) or absent → connection host.
- [x] AC-2 — Behind the header, two clients with different header IPs each get their own 30-per-minute login budget (no shared limit).
- [x] AC-3 — Register: 5 attempts from one IP within an hour pass to normal handling; the 6th → `429 TOO_MANY_REQUESTS`; another IP is not affected; after the hour it works again (fake clock).
- [x] AC-4 — The access log line has `client_ip`.
- [x] AC-5 — Config: invalid header name rejected with the variable named.
- [x] AC-6 — On DigitalOcean (after deploy), `CLIENT_IP_HEADER=do-connecting-ip` is set and the access log shows the caller's public IP, not the proxy's (recorded in implementation.md).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-4 | unit | `TestClientIP` (table), `TestAccessLog_ClientIP` |
| AC-2, AC-3 | unit (handler + fake clock) | `TestLogin_PerClientIPBehindProxy`, `TestRegister_RateLimited` |
| AC-5 | unit | `TestConfig_ClientIPHeader` |
| AC-6 | manual on DO | doctl logs |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Client IP source | configurable header / always X-Forwarded-For | Answered | `CLIENT_IP_HEADER`, `do-connecting-ip` on DO |
| 2 | Register limit | 5/IP/h / 20/IP/h | Answered | 5 per IP per hour |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Client IP from `CLIENT_IP_HEADER`; registration 5/IP/hour; MVP auth scope (TDR-018) | Yahya | TDR-018, TDR-011, PRD-001 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | Approved | Implemented and reviewed (no findings) |
| 2026-11-10 | Done | Finalized — PR opened |

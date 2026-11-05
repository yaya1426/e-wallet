# TDR-011 — Authentication (Minimal, Phase 1)

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning; PRD-001 Q2 answered: this proposal) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-001 §7 |

## Context
Every money endpoint must know *who* is acting. Full identity (OTP, KYC, device binding) is
out of scope, but we should not build flows on a fake identity that would be hard to replace.

## Decision (proposed)
- Module `internal/identity` owns `users` (`id, phone_e164 UNIQUE, full_name, password_hash,
  status, created_at`) and `bank_accounts` (`user_id UNIQUE, iban, holder_name, created_at`).
- `POST /v1/auth/register` — phone (E.164, Egyptian `+20`), full name, password (min 8).
  Creates user **and** wallet ledger account in one DB transaction.
- `POST /v1/auth/login` → JWT access token (HS256, 15 min TTL, claims `sub=user_id`, `exp`,
  `iat`, `jti`). Secret from env `JWT_SECRET`.
- Passwords: `bcrypt` cost 12 (`golang.org/x/crypto/bcrypt`).
- JWT library: `github.com/golang-jwt/jwt/v5`.
- HTTP middleware validates the token and puts `user_id` into the request context.
- Internal/operator endpoints (`/internal/v1/*`) use a static API key from env in this phase.
- Basic rate limit on login (per phone + per IP, in memory) to slow brute force.

Not in phase 1: refresh tokens, OTP, password reset, sessions revocation, roles beyond
user/operator.

## Alternatives
| Option | Notes |
|--------|-------|
| No auth, `X-User-ID` header | Fastest; only acceptable for local dev; easy to forget to remove. |
| External IdP (Keycloak / Auth0) | Right for production; heavy for now. The middleware boundary makes it swappable. |

## Consequences
- + Real identity boundary from day one; swappable later behind the middleware.
- − Some code that is not core to the wallet problem.

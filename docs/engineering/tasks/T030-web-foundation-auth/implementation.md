# T030 — Implementation

| Field | Value |
|-------|-------|
| Date | 2026-11-12 |
| Branch | `task/T030-web-foundation-auth` |
| Result | All automated checks pass; AC-20 (CI on the PR) and AC-21 (DigitalOcean) wait for the PR and the owner's go-ahead |

## Summary
New `web/` app per TDR-019: Vite 8 + React 19 + TypeScript 5.9 (strict), React Router 7, TanStack
Query 5, Tailwind 4, `openapi-fetch` over types generated from `api/openapi/wallet.yaml`. Session
handling per TDR-020 (`web/src/auth/session.ts`), a typed client that adds the token and recovers
from 401 (`web/src/api/client.ts`), screens for sign-up, log-in, password reset, a placeholder home
(balance + sign out) and 404. Make targets `web-*`, CI jobs `web` and `web-e2e`. No Go code changed.

### Files
| Path | What |
|------|------|
| `web/package.json`, `package-lock.json`, `.nvmrc` (24), `tsconfig*.json`, `vite.config.ts`, `eslint.config.js`, `playwright.config.ts` | Tooling. ESLint bans `dangerouslySetInnerHTML` and cross-feature / `../` imports in `src/features`. |
| `web/csp.ts`, `web/scripts/check-dist.ts` | CSP string; Vite plugin injects it into the built `index.html`; `npm run build` fails if it is missing/different or if any inline script/style exists. |
| `web/src/api/{schema.d.ts,client.ts,errors.ts}` | Generated types; auth fetch wrapper; `ApiError`, `unwrap`. |
| `web/src/auth/{session.ts,AuthProvider.tsx,guards.tsx,next.ts}` | Session store (TDR-020), React context (`useAuth`, `useApi`), `RequireAuth` / `RedirectIfAuthed`, `safeNext`. |
| `web/src/features/auth/*`, `features/home/HomePlaceholder.tsx`, `features/NotFoundPage.tsx` | Screens. |
| `web/src/lib/{phone,money,validation,errorMessages,useSubmit,focus}.ts` | Helpers (`formatMoney`, `parseAmount`, `normalizeEgyptianMobile`, `messageForError`). |
| `web/src/components/*`, `web/src/i18n/en.ts` | Shared UI; every string and `en.errors` keyed by API code. |
| `web/e2e/*` | Playwright specs. |
| `web/go.mod` | Keeps `go ... ./...` out of `web/` (npm package `flatted` ships Go files that `go test ./...` picked up). |
| `Makefile` | `web-install`, `web-dev`, `web-gen-api`, `web-gen-check`, `web-lint`, `web-test`, `web-build`, `web-e2e`, `web-ci`; `fmt`/`fmt-check` now skip `web/`. |
| `.github/workflows/ci.yml` | Jobs `web`, `web-e2e`. |
| `.gitignore`, `.dockerignore` | web build output / `web/` excluded from the Go image. |
| Docs | PRD-005, TDR-019, TDR-020, PRD-001 §3/§4, architecture §2, docs/README.md, tasks README. |

## AC → tests
| AC | Tests | Result |
|----|-------|--------|
| 1 | `make web-ci` (gen-check, lint, typecheck, 141 Vitest tests, build + check-dist) | ✅ |
| 2 | `lib/phone.test.ts` (8 accepted, 9 rejected) | ✅ |
| 3 | `lib/money.test.ts` formatMoney + parseAmount | ✅ |
| 4 | `lib/validation.test.ts` | ✅ |
| 5 | e2e "sign up lands on home" (desktop + mobile); `RegisterPage.test.tsx` | ✅ |
| 6 | e2e "a registered phone cannot sign up again"; component test | ✅ |
| 7 | `RegisterPage.test.tsx` "shows all client errors and sends nothing", "mismatched confirmation" | ✅ |
| 8 | e2e "login works and a wrong password is refused" (desktop + mobile); `LoginPage.test.tsx` | ✅ |
| 9 | e2e "a reload keeps the user signed in" (also asserts rotation); `session.test.ts` restore cases | ✅ |
| 10 | `client.test.ts` "three parallel calls … share one refresh", "late 401 … without another refresh"; `session.test.ts` single-flight | ✅ |
| 11 | `session.test.ts` "two tabs … serialized and never reuse a token"; e2e "two tabs reloading at the same time" (3 rounds + third tab) | ✅ |
| 12 | `session.test.ts` refresh 401 / other 4xx; `client.test.ts`; `LoginPage.test.tsx` notice | ✅ |
| 13 | `session.test.ts` 403; `client.test.ts` "403 USER_DISABLED from any endpoint"; `LoginPage.test.tsx` | ✅ |
| 14 | e2e "sign out ends the session in every tab and on the server" (old refresh token → 401); `session.test.ts` logout cases; `HomePlaceholder.test.tsx` | ✅ |
| 15 | e2e "password reset with an operator code" (wrong code, real code from `/internal/v1`, old password refused, new works); `ResetPasswordPage.test.tsx` | ✅ |
| 16 | `guards.test.tsx` (redirect + return, off-site `next` incl. `/\t/`, `/\`, `//`) | ✅ |
| 17 | `LoginPage.test.tsx` 429 / 403 / 500 with reference / unknown code / network | ✅ |
| 18 | `scripts/check-dist.ts` in `npm run build`; e2e smoke reads the CSP meta | ✅ |
| 19 | Component tests query by label/role; `LoginPage.test.tsx` keyboard-only flow; `aria-describedby` asserted | ✅ |
| 20 | CI on the PR | ⏳ after PR |
| 21 | Deployment | ⏳ owner go-ahead |

## Verification
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `make tidy-check` | ✅ no diff |
| 2 | Format | `make fmt-check` | ✅ |
| 3 | Vet | `make vet` | ✅ |
| 4 | Lint | `make lint` (golangci-lint v2.15.0, built with Go 1.27) | ✅ 0 issues |
| 5 | Build | `make build` | ✅ |
| 6 | Unit | `make test` (Go, `-race`) | ✅ all packages ok |
| 7 | Integration | `go test -race -count=1 -tags integration ./...` (testcontainers) | ✅ all packages ok |
| 8 | Web checks | `make web-ci` | ✅ ESLint 0 problems, `tsc` ok, Vitest 10 files / 141 tests, build ok, check-dist ok |
| 9 | Run the stack | Postgres (compose) + `wallet serve` + `banksim` built from this branch, `/readyz` 200 | ✅ |
| 10 | Browser checks | `make web-e2e` → Playwright, 15 tests (desktop 1280×720 + mobile 390×844) | ✅ 15/15 |
| 11 | Ledger integrity | not applicable — no money moves in this task | — |
| 12 | Tear down | stack stopped | ✅ |

Environment note: in this run the Go images could not be built inside Docker (the build sandbox's
TLS proxy is not trusted inside `docker build`), so step 9 ran the same binaries natively against
the compose Postgres with the compose environment values. CI runs `make up` as usual.

Screenshots (desktop + mobile: login, sign-up errors, home, reset) were checked by eye: layout holds
from 390 px, errors sit under their fields, the balance reads `EGP 0.00`.

## Review fixes (round 1, before code review)
An independent read found 1 Major and several Minor issues; all were fixed and covered by tests:
`safeNext` now resolves against the origin (tabs/newlines/backslashes could reach an external URL
and crash the router); a refresh in flight can no longer revive an ended session (generation
guard); any 4xx from refresh except 429 is terminal; session requests time out after 15 s; refresh
delay never < 5 s; a late 401 for an old token reuses the newer token; an expired token whose
refresh fails surfaces as NETWORK_ERROR instead of a false "session ended"; field errors are in an
`aria-live` region; button spinner is decorative; reset code input keeps only digits; sign-out lands
on `/login`; `web-gen-check` fails if the generated file is untracked.

## Deviations
- CSP meta is injected into the **built** `index.html` (dev server runs without it). TDR-020 §4 was
  clarified in this PR; the policy itself is unchanged.
- `web/go.mod` and the `fmt` target change were not in the plan; both only keep Go tooling out of
  `web/node_modules`.
- `npm audit`: 0 vulnerabilities in runtime dependencies; 10 moderate in dev-only lint tooling
  (`minimatch` via eslint/typescript-eslint, ReDoS on glob patterns we control). No fix released yet.

## Follow-ups
- Login limit is 30/IP/min and fixed in code; the e2e suite is sized to stay under it (mobile runs
  only `@mobile` + smoke tests).
- `frame-ancestors`/HSTS need response headers (TDR-020 Revisit).

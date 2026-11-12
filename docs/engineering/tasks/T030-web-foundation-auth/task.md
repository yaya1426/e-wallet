# T030 — Web app foundation + auth screens

| Field        | Value |
|--------------|-------|
| ID           | T030 |
| Status       | Approved |
| Created      | 2026-11-12 |
| Branch       | `task/T030-web-foundation-auth` |
| Depends on   | T025 (refresh/logout), T027 (password reset) — both Done |
| PRDs         | PRD-005 §5.1–5.2 (UI-BR-1..12), §6; PRD-001 §3 (non-goal lifted) |
| TDRs         | TDR-019, TDR-020, TDR-011, TDR-013, TDR-017, TDR-018 §3–4 |
| Docs changed | PRD-005 (new), TDR-019 (new), TDR-020 (new; §4 clarified during review), PRD-001 §3/§4, architecture.md §2, docs/README.md |

## What
The first slice of the web app (PRD-005 area 1): a working React app in `web/` with its build,
tests and CI, a typed API client, browser session handling, and the four auth flows. Later UI
tasks (areas 2–5) build on it.

Deliverables:
- `web/` scaffold per TDR-019 §2 (Vite + React + TS strict, Router, TanStack Query, Tailwind, ESLint, Vitest, Playwright).
- Generated `web/src/api/schema.d.ts` + `openapi-fetch` client with auth middleware and typed error parsing.
- Session module (TDR-020): in-memory access token, refresh token in `localStorage`, Web-Locks refresh, proactive/reactive refresh, cross-tab logout.
- Screens: **Sign up**, **Log in**, **Reset password**, a protected **Home placeholder** (shows the wallet balance from `GET /v1/me/wallet` and a **Sign out** button), **Not found**.
- Shared pieces: `lib/phone.ts`, `lib/money.ts`, `lib/validation.ts`, `i18n/en.ts`, `components/` (TextField, PasswordField, Button, Alert, PageLayout).
- Make targets, CI jobs `web` and `web-e2e`, `.dockerignore` entry, `web/README.md`.
- DigitalOcean: static-site component and route changes (TDR-019 §3) — **applied only after the owner's go-ahead** (production change).

## Why
PRD-005 UI-G1/G3/G4 and UI-BR-1..12; TDR-019 (stack, layout, same origin); TDR-020 (sessions).
Every later UI area needs the client, session and shared helpers built here.

## How
### Modules and files
| Path | Change |
|------|--------|
| `web/package.json`, `package-lock.json` | Scripts: `dev`, `build` (`tsc -b && vite build`), `preview`, `lint`, `typecheck`, `test` (vitest run), `test:e2e` (playwright), `gen:api` (openapi-typescript ../api/openapi/wallet.yaml -o src/api/schema.d.ts). `engines.node: ">=24"`; `.nvmrc` = 24. |
| `web/vite.config.ts` | React + Tailwind plugins; dev **and preview** server proxy `/v1`, `/internal`, `/healthz` → `process.env.WALLET_API_URL ?? http://localhost:8080`; Vitest config (jsdom, setup file). |
| `web/index.html` | CSP meta exactly as TDR-020 §4; `<html lang="en" dir="ltr">`; viewport meta. |
| `web/src/main.tsx`, `src/app/{App,router,providers}.tsx` | QueryClient (retry: 0 for mutations, 1 for queries except on 4xx), AuthProvider, RouterProvider. |
| `web/src/api/client.ts` | `createClient<paths>({baseUrl: '/'})`; middleware sets `Authorization: Bearer <access>` for non-`/v1/auth/{login,register,refresh,password-reset}` paths; on `401` with code `UNAUTHENTICATED` calls `session.refresh()` once and retries the request once. |
| `web/src/api/errors.ts` | `ApiError {status, code, requestId}` parsed from the `Error` schema; `NETWORK_ERROR` when fetch throws; `UNKNOWN` when the body isn't the schema. |
| `web/src/auth/session.ts` | Framework-free store (TDR-020): `getAccessToken`, `login(phone,pw)`, `restore()`, `refresh()` (single in-tab promise + `navigator.locks` cross-tab, re-reading storage inside the lock), `logout()`, `subscribe(listener)`, proactive timer at `expires_in − 60 s`, `storage` event listener. Storage key `ewallet.session.v1`. Injectable `fetch`, `storage`, `locks`, `now` for tests. |
| `web/src/auth/{AuthProvider,RequireAuth,RedirectIfAuthed}.tsx` | Context exposing `status: 'loading'│'authenticated'│'anonymous'`, `endReason` (message key for UI-BR-6/12). `RequireAuth` → `/login?next=<path>`; `RedirectIfAuthed` sends signed-in users from auth pages to `/`. |
| `web/src/features/auth/{LoginPage,RegisterPage,ResetPasswordPage}.tsx` | Forms per UI-BR-7/8/10; disable submit while pending; field errors under inputs (`aria-describedby`), form error in an `Alert` (`role="alert"`). |
| `web/src/features/home/HomePlaceholder.tsx` | `useQuery(getMyWallet)`; shows "Available balance" with `formatMoney`, status badge when `FROZEN`, Sign out. Replaced by area 2. |
| `web/src/features/NotFoundPage.tsx` | 404 with link home. |
| `web/src/lib/phone.ts` | `normalizeEgyptianMobile(input): string│null` per UI-BR-2. |
| `web/src/lib/money.ts` | `formatMoney(minor: number): string` → `EGP 1,234.56`, negative as `-EGP 1.50`; integer-only arithmetic; throws on non-safe-integer. `parseAmount(text): {ok:true, minor:number} │ {ok:false, error:'empty'│'format'│'tooManyDecimals'│'zero'│'tooLarge'}`: trims, removes `,` and spaces, accepts `^\d+(\.\d{1,2})?$`, converts with string/integer math (no floats), rejects 0 and anything above `Number.MAX_SAFE_INTEGER` piasters. Business limits (PRD-001 §5.6) stay server-side. |
| `web/src/lib/validation.ts` | `validatePassword` (8–72 UTF-8 bytes via `TextEncoder`), `validateFullName` (2–100 chars after trim, counted as code points), `validateResetCode` (`^\d{6}$`), `passwordsMatch`. Return message keys. |
| `web/src/lib/errorMessages.ts` | `messageForError(err)` using `en.errors` (table below). |
| `web/src/i18n/en.ts` | All strings (UI-BR-9). |
| `web/src/components/*` | TextField, PasswordField (show/hide toggle), Button (loading state), Alert (error/success/info), PageLayout (centered card, app name header). |
| `web/e2e/*.spec.ts`, `playwright.config.ts` | Runs against `vite preview` (proxy → compose `wallet`); chromium + mobile-webkit-viewport (chromium, 390×844) projects. |
| `web/README.md` | Setup, scripts, how to run against compose. |
| `Makefile` | `web-install` (npm ci), `web-dev`, `web-gen-api`, `web-gen-check` (regenerate + `git diff --exit-code web/src/api/schema.d.ts`), `web-lint` (lint + typecheck), `web-test`, `web-build`, `web-e2e`. |
| `.github/workflows/ci.yml` | Job `web`: setup-node 24 with npm cache → `make web-install web-gen-check web-lint web-test web-build`. Job `web-e2e`: `make up`, `make web-install`, `npx playwright install --with-deps chromium`, `make web-e2e`; upload Playwright report on failure. |
| `.dockerignore` | Add `web/`. |
| `.gitignore` | `web/node_modules`, `web/dist`, `web/playwright-report`, `web/test-results`. |

### Data (migrations)
None.

### API
None changed. Used: `POST /v1/auth/register|login|refresh|logout|password-reset`, `GET /v1/me/wallet`;
e2e also uses `POST /internal/v1/users/{phone}/password-reset-code` (compose `INTERNAL_API_KEY`).

### Behaviour and design notes
**Routes:** `/login`, `/register`, `/reset-password` (wrapped in `RedirectIfAuthed`), `/` (`RequireAuth` → HomePlaceholder), `*` → NotFound. While `status==='loading'` (startup restore) show a centered spinner, no redirect.

**Startup (`restore`)**: read storage; missing / unparseable / `refresh_expires_at` ≤ now → clear, `anonymous`. Otherwise `refresh()`; success → `authenticated`; `401 INVALID_REFRESH_TOKEN` → clear, `anonymous`, endReason `sessionEnded`; `403 USER_DISABLED` → clear, `anonymous`, endReason `userDisabled`; network/5xx → keep storage, `anonymous` with endReason `network` (login page shows "Can't reach the server" + Retry which re-runs `restore`).

**Login:** normalize phone (invalid → field error, no request). `200` → store tokens (`refresh_expires_at = now + refresh_expires_in*1000`), set access + expiry, schedule proactive refresh, `authenticated`, navigate to `next` if it is a same-app path starting with `/` and not `//`, else `/`.

**Register:** validate all fields client-side first (order phone → full name → password → confirm, but show all field errors at once). `201` → `login()` with the same credentials → `/`. If that login fails → `/login` with phone prefilled (router state) and info "Account created, please log in". Server field codes map to the matching field: `INVALID_PHONE`→phone, `INVALID_NAME`→full name, `INVALID_PASSWORD`→password, `PHONE_ALREADY_REGISTERED`→phone ("This phone number is already registered. Log in instead." with link).

**Reset password:** fields phone, code, new password, confirm. `204` → `/login` with success "Password changed. Please log in." and phone prefilled. Local session (if any stored) is cleared on success (server revoked it).

**Refresh (TDR-020 §2):** `refresh()` returns the in-tab in-flight promise if one exists; otherwise inside `navigator.locks.request('ewallet-refresh', {mode:'exclusive'})`: re-read storage → if gone: end session (`sessionEnded`); POST refresh; on 200 write storage **before** releasing the lock. If `navigator.locks` is undefined (old browser), fall back to in-tab single flight only. Proactive timer: `max(0, expiresAt − now − 60 000)`; cleared on logout. Timer refresh failures: network → retry after 30 s while the access token is still valid; else end session.

**API 401 handling:** middleware only refreshes on `401` + `UNAUTHENTICATED` for authenticated paths; retries once with the new token; a second 401 ends the session (`sessionEnded`). `403 USER_DISABLED` from any endpoint ends the session (`userDisabled`). Request bodies must be re-sendable (clone the Request before the first send).

**Logout:** `POST /v1/auth/logout` with the bearer + `{refresh_token}`; ignore its result (including network error); clear memory, storage, QueryClient cache; navigate to `/login`. Other tabs: `storage` event with `newValue === null` → clear memory + cache → `/login`.

**Error message table (`en`):**
| Code | Message |
|------|---------|
| `INVALID_CREDENTIALS` | Wrong phone number or password. |
| `INVALID_PHONE` | Enter a valid Egyptian mobile number, like 01001234567. |
| `INVALID_PASSWORD` | Password must be 8 to 72 characters. |
| `INVALID_NAME` | Name must be 2 to 100 characters. |
| `PHONE_ALREADY_REGISTERED` | This phone number is already registered. Log in instead. |
| `INVALID_RESET_CODE` | The code is wrong or expired. Ask support for a new one. |
| `TOO_MANY_REQUESTS` | Too many attempts. Please wait and try again. |
| `USER_DISABLED` | Your account is disabled. Please contact support. |
| `INVALID_REFRESH_TOKEN` / session end | Your session has ended. Please log in again. |
| `REQUEST_TOO_LARGE`, `MALFORMED_REQUEST`, `INTERNAL_ERROR`, `TRY_AGAIN`, unknown | Something went wrong. Please try again. (Reference: <request_id>) |
| `NETWORK_ERROR` | Can't reach the server. Check your connection and try again. |

The table lives in `i18n/en.ts` as `en.errors: Record<string,string>` keyed by API code; feature tasks extend it by adding keys. `lib/errorMessages.ts` exposes `messageForError(err: ApiError): string` (code lookup, else generic + reference).

**Accessibility:** inputs have `<label>`, `autocomplete` (`tel`, `name`, `current-password`, `new-password`, `one-time-code`), `inputmode="tel"`/`"numeric"`; focus moves to the first invalid field on submit; the form-level Alert is `role="alert"`.

**DigitalOcean (finalize step, owner go-ahead required):** read the current app spec (`doctl apps spec get`), save it to `docs/engineering/tasks/T030-web-foundation-auth/do-app-spec.before.yaml` (secrets redacted), add the `web` static site and explicit wallet routes per TDR-019 §3, `doctl apps update --spec`, then verify: `/` serves the app, `/readyz` and `/v1/auth/login` still reach the wallet, a hard reload on `/register` serves the app (catch-all), and the full login → home → sign out flow works in a browser.

### Out of scope
Wallet home design and history (area 2), money input and bank flows (3), transfers (4), operator console (5); Arabic/RTL; httpOnly-cookie sessions; backend changes.

## Acceptance Criteria
- [x] AC-1 — Given a fresh clone, when `make web-install web-gen-check web-lint web-test web-build` runs, then all pass and `web/dist` is produced; `web-gen-check` fails if `wallet.yaml` changed without regenerating.
- [x] AC-2 — `normalizeEgyptianMobile` accepts `01001234567`, `201001234567`, `+20 100 123 4567`, `0100-123-4567` → `+201001234567`; rejects `0130…`, 10/12-digit numbers, letters, empty.
- [x] AC-3 — `formatMoney`: 0 → `EGP 0.00`, 5 → `EGP 0.05`, 15075 → `EGP 150.75`, 123456789 → `EGP 1,234,567.89`, −150 → `-EGP 1.50`; non-integer / unsafe integer throws. `parseAmount`: `150`→15000, `150.5`→15050, `1,234.56`→123456, `0.01`→1; `` / `abc` / `1.234` / `-5` / `0` / `0.00` rejected with the right error.
- [x] AC-4 — Validation: password of 7 bytes rejected, 8 accepted, 72 accepted, 73 rejected (incl. a multi-byte case: 37 × "é" = 74 bytes rejected); name "  A " rejected, "Al" accepted, 101 chars rejected; code `12345`/`12a456` rejected, `123456` accepted.
- [x] AC-5 — Given a new phone, when the user signs up with valid data, then they land on Home showing `EGP 0.00` (e2e).
- [x] AC-6 — Given an already registered phone, when signing up, then the phone field shows the "already registered" message with a link to login (component + e2e).
- [x] AC-7 — Given invalid client input (bad phone, short password, mismatched confirmation), when submitting sign-up, then field errors show and **no request** is sent (component).
- [x] AC-8 — Given a registered user, when logging in with the right password, then Home shows the balance; with a wrong password, "Wrong phone number or password." (e2e).
- [x] AC-9 — Given a signed-in user, when the page reloads, then they stay on Home (restored via refresh) (e2e).
- [x] AC-10 — Given an access token that expired, when an API call returns 401 `UNAUTHENTICATED`, then exactly one refresh happens and the call is retried and succeeds (unit with fake fetch: 3 parallel calls → 1 refresh).
- [x] AC-11 — Given two "tabs" (two session instances sharing storage and a lock), when both refresh at the same time, then the refresh calls are serialized and the second uses the token written by the first (no reuse; unit). And e2e: two pages in one context, reload both, both stay signed in.
- [x] AC-12 — Given refresh returns 401 `INVALID_REFRESH_TOKEN`, then storage is cleared and login shows "Your session has ended…" (unit + component).
- [x] AC-13 — Given any call returns 403 `USER_DISABLED`, then the session ends and login shows the disabled message (unit/component; e2e via operator freeze is **not** applicable since freeze ≠ disable — component test only).
- [x] AC-14 — Given a signed-in user, when they sign out, then `/v1/auth/logout` is called, storage is cleared, they see Login, and a second open page goes to Login too; the old refresh token no longer works (e2e asserts refresh with the old token → 401).
- [x] AC-15 — Given a reset code issued through the internal API, when the user resets the password, then they land on Login with the success notice, can log in with the new password and not the old one (e2e). Wrong code → "The code is wrong or expired…" (e2e).
- [x] AC-16 — Given an anonymous visitor opening `/`, then they are sent to `/login?next=%2F` and after login return to `/`; `next=//evil.example` is ignored (component).
- [x] AC-17 — 429 on login shows the rate-limit message; network failure shows the connection message; unknown 500 shows the generic message with the request id (component, mocked fetch).
- [x] AC-18 — `index.html` contains the exact CSP of TDR-020 §4 and the built app has no inline `<script>` (build check in a unit test reading `dist/index.html`, or a script in `web-build`).
- [x] AC-19 — Forms are usable with the keyboard only and every input has an accessible name (component tests query by label/role).
- [ ] AC-20 — CI jobs `web` and `web-e2e` pass on the PR.
- [ ] AC-21 — (After owner go-ahead) Deployed: the app loads at the DO app URL, login → home → sign out works, `/readyz` and the API still work, deep link `/register` loads.

## Test Plan
| AC | Type | Test |
|----|------|------|
| 1 | CI / make | `web` job |
| 2 | unit | `lib/phone.test.ts` table test |
| 3 | unit | `lib/money.test.ts` |
| 4 | unit | `lib/validation.test.ts` |
| 5 | e2e | `e2e/auth.spec.ts` "sign up lands on home" |
| 6 | component + e2e | `RegisterPage.test.tsx` "duplicate phone"; e2e "duplicate phone" |
| 7 | component | `RegisterPage.test.tsx` "client validation blocks submit" |
| 8 | e2e | "login ok / wrong password" |
| 9 | e2e | "reload keeps session" |
| 10 | unit | `auth/session.test.ts` "parallel 401s share one refresh" |
| 11 | unit + e2e | `session.test.ts` "two instances serialize via lock"; e2e "two tabs reload" |
| 12 | unit + component | `session.test.ts` "invalid refresh ends session"; `LoginPage.test.tsx` "session ended notice" |
| 13 | unit + component | `session.test.ts` / `client.test.ts` "403 USER_DISABLED ends session" |
| 14 | e2e | "sign out ends session in all tabs and server-side" |
| 15 | e2e | "password reset with operator code"; "wrong code" |
| 16 | component | `RequireAuth.test.tsx` "redirect + next sanitization" |
| 17 | component | `LoginPage.test.tsx` error mapping cases |
| 18 | unit | `csp.test.ts` (reads `index.html`; `dist` check runs after build in `web-build`) |
| 19 | component | queries by label/role in page tests; tab-order test on LoginPage |
| 20 | CI | PR checks |
| 21 | manual + recorded | `implementation.md` deployment notes (browser check via Playwright against the DO URL: `WEB_BASE_URL=https://… npx playwright test e2e/smoke.spec.ts`) |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| Q1 | Web or mobile? | Web SPA / RN / Flutter | Answered | Web SPA |
| Q2 | Stack? | React+TS+Vite… / Next.js / Go+HTMX | Answered | React+TS+Vite, Router, TanStack Query, Tailwind, openapi-typescript, Vitest, Playwright |
| Q3 | Where/how served? | `web/` + DO static site same origin / embed in Go / separate domain + CORS | Answered | `web/` + DO static-site component, same origin |
| Q4 | Token storage? | access in memory + refresh in localStorage with CSP / httpOnly cookie (backend change) | Answered | Memory + localStorage + strict CSP |
| Q5 | Language? | English, strings centralised / EN+AR RTL | Answered | English only, strings centralised |
| Q6 | Task split? | 5 areas | Answered | 5 areas (PRD-005 §4) |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Q1–Q6 as above ("all recommended") | Yahya | PRD-005, TDR-019, TDR-020, PRD-001 §3/§4, architecture.md §2, docs/README.md |
| 2026-11-12 | After sign-up the UI logs in automatically (register returns no token) | Recommended, accepted with the plan | PRD-005 UI-BR-7 |
| 2026-11-12 | Phone typed as 01…, 201… or +201… is normalised client-side | Recommended, accepted with the plan | PRD-005 UI-BR-2 |
| 2026-11-12 | Plain React forms, no form library; `openapi-fetch` client | Recommended, accepted with the plan | TDR-019 §1 |
| 2026-11-12 | DO route change is a production change, applied only after Yahya's go-ahead | Yahya (via coordinator) | this task |
| 2026-11-12 | CSP meta injected into the built index.html only (dev server needs inline code) | Review finding #9 | TDR-020 §4 (clarified) |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Ready | Planned; all questions answered by Yahya ("all recommended") |
| 2026-11-12 | In Review | Implemented; web-ci, Go CI checks and 15 Playwright tests pass |
| 2026-11-12 | Approved | Review round 1: 1 Major + 13 Minor/Nit, all fixed; round 2 approved |

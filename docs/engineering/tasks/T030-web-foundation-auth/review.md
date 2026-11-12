# Review — T030 (2026-11-12, round 1)
Verdict: Changes Requested
Summary: An independent read of every `web/` file, the Makefile targets and the CI jobs against
task.md, PRD-005 §5.1–5.2, TDR-019/020 and TDR-018 §3–4. The refresh-token handling was correct
(single-flight, locks, storage re-read inside the lock), but a crafted `next` could break login,
and several session edge cases could leave the user in the wrong state.

| # | Severity | File:line | Rule | Finding | Fix | Status |
|---|----------|-----------|------|---------|-----|--------|
| 1 | Major | web/src/auth/next.ts | PRD-005 UI-BR-8, security | `safeNext` let `/\t/evil.example` and `/\n\evil.example` through. URL parsing turns these into an external URL and React Router throws on login. | Resolve against the origin, require the same origin, reject control characters and `\` | Fixed |
| 2 | Minor | web/src/auth/session.ts | TDR-020 §2 | A refresh in flight could revive a session that `end()` had already ended | Generation counter; stale results are dropped | Fixed |
| 3 | Minor | web/src/api/client.ts | UI-BR-4 | If the token had expired and the refresh then failed, the request went out with no token and the UI showed "session ended" | Throw the refresh error instead | Fixed |
| 4 | Minor | web/src/api/client.ts | TDR-020 §2 | A late 401 started a needless extra rotation | Reuse a newer token if one exists | Fixed |
| 5 | Minor | web/src/auth/session.ts | robustness | `JWT_TTL ≤ 60 s` caused a refresh loop | 5 s floor | Fixed |
| 6 | Minor | web/src/auth/session.ts | robustness | A hung refresh held the lock forever | 15 s `AbortSignal.timeout` | Fixed |
| 7 | Minor | web/src/auth/session.ts | UI-BR-12 | A refresh 400/413 left the user stuck on "can't reach server" | Any 4xx except 429 is terminal | Fixed |
| 8 | Minor | web/src/components/TextField.tsx | UI-NFR-2 | Field errors were not announced | `aria-live` region | Fixed |
| 9 | Minor | web/index.html, csp.ts | TDR-020 §4 | The CSP is added only at build time, which the TDR did not say | TDR-020 §4 clarified; build check enforces it | Fixed (doc) |
| 10 | Minor | Makefile | CI | `web-gen-check` ignored an untracked file | `git ls-files --error-unmatch` | Fixed |
| 11 | Nit | HomePlaceholder.tsx, guards.tsx | task.md | Sign out had no pending state and landed on `/login?next=%2F` | Loading state; plain `/login` after sign-out | Fixed |
| 12 | Nit | LoginPage.tsx | PRD-005 | "Account created" shown as success | info tone | Fixed |
| 13 | Nit | Button.tsx | a11y | The spinner's label polluted the button name | `decorative` spinner | Fixed |
| 14 | Nit | ResetPasswordPage.tsx | UX | `maxLength` cut a pasted code | Keep digits only, max 6 | Fixed |
| 15 | Nit | playwright.config.ts | task.md | Mobile viewport was not 390×844 | Set explicitly | Fixed |
| 16 | Nit | lib/money.ts | PRD-005 | parseAmount cap vs UI-BR-21 | Aligned with the T031–T034 planning thread (UI-BR-21 now defers to parseAmount) | Closed |
| 17 | Nit | AuthProvider.tsx | React | Inline subscribe re-subscribed on every render | `useCallback` | Fixed |

Also found while verifying: `go test ./...` picked up Go files inside `web/node_modules`. Added
`web/go.mod` and moved `fmt`/`fmt-check` off `web/`. Fixed.

# Review — T030 (2026-11-12, round 2)
Verdict: Approved
Summary: Every round-1 finding is fixed and covered by new tests (session +7, client +3, guards +6,
reset +1, home: sign-out lands on `/login`). Full verification was re-run: Go tidy, fmt, vet,
golangci-lint (0 issues), build, unit and integration tests pass; `make web-ci` passes (141 tests);
Playwright 15/15 pass against the running stack.

Checked and fine: module boundaries (ESLint enforces no cross-feature imports); no money arithmetic
outside `lib/money.ts`; tokens never written to storage except the refresh token; public paths never
carry the bearer; error-code table matches the OpenAPI catalogue; CI jobs use the repo's pinned
action majors; no Go code changed.

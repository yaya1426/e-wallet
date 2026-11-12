# TDR-019 — Web Front-end: Stack, Repo Layout and Same-Origin Hosting

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-12, UI planning) |
| Date       | 2026-11-12 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-005, TDR-012, TDR-013, TDR-015, TDR-020 |

## Context
PRD-005 adds a web app on top of the existing JSON API. We need a stack, a place in the repo,
a way to keep the UI's types in line with `api/openapi/wallet.yaml`, and a way to serve it
without changing the Go service (no CORS support exists today).

## Decision
### 1. Stack
- **TypeScript (strict) + React + Vite**, single-page app. Node **24 LTS**, **npm** (lockfile
  `package-lock.json` checked in). Exact versions are pinned by the lockfile when the foundation
  task is implemented.
- **React Router** (routing), **TanStack Query** (server state, caching, loading/error states),
  **Tailwind CSS** (styling). No global state library; the session lives in a React context (TDR-020).
- **API types:** `openapi-typescript` generates `web/src/api/schema.d.ts` from
  `api/openapi/wallet.yaml`; requests go through **`openapi-fetch`** typed with it. The generated
  file is checked in and CI fails when it is out of date (same idea as `sqlc-check`).
- **Forms:** plain React forms with small validation functions in `web/src/lib/` (no form library).
- **Tests:** Vitest + Testing Library (unit/component, jsdom), Playwright (end to end against the
  compose stack). ESLint (typescript-eslint, react-hooks) + `tsc --noEmit`.

### 2. Layout
Everything lives in **`web/`** in this repo (one PR can change the API and the UI together):
```
web/
  index.html            CSP meta tag (TDR-020)
  package.json  package-lock.json  vite.config.ts  tsconfig*.json  eslint.config.js
  playwright.config.ts  e2e/
  src/
    main.tsx  app/        router, shell, providers
    api/                  schema.d.ts (generated), client.ts, errors.ts
    auth/                 session store, provider, route guard (TDR-020)
    features/<area>/      one folder per PRD-005 area (auth, home, money, transfer, ops)
    components/           shared UI pieces
    lib/                  phone, money, validation helpers
    i18n/en.ts            all user-facing strings
```
Feature folders import from `api`, `auth`, `components`, `lib`, `i18n`, never from another
feature folder. `web/` is listed in `.dockerignore` so the Go image does not change.

### 3. Same-origin hosting
- **Local:** `npm run dev` (Vite) proxies `/v1`, `/internal` and `/healthz` to `http://localhost:8080`
  (the compose `wallet`). The browser only talks to the Vite origin.
- **DigitalOcean:** the existing App Platform app gets a **static-site component `web`**
  (`source_dir: web`, build `npm ci && npm run build`, `output_dir: dist`,
  `catchall_document: index.html` for client-side routes) on route `/`. The wallet service keeps
  explicit routes (`/v1`, `/internal`, `/webhooks`, `/healthz`, `/readyz`, `/metrics` as today) with
  `preserve_path_prefix: true`. UI and API therefore share one domain: no CORS, and the backend
  code does not change.
- The API base URL in the app is always relative (`/`).

### 4. Make and CI
Make targets `web-install`, `web-dev`, `web-gen-api`, `web-gen-check`, `web-lint`, `web-test`,
`web-build`, `web-e2e`. CI gets a `web` job (gen-check, lint, typecheck, unit tests, build) and a
`web-e2e` job (compose stack + Playwright). Both become required checks on `main`.

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| Next.js | SSR, file routing | Needs a Node server or extra hosting; SSR adds nothing behind a login | More moving parts for no gain |
| Go templates + HTMX | One language, no build step | Weaker client-side state (sessions, tabs), less reusable for later mobile work | User preferred a real SPA |
| Separate repo | Independent releases | API and UI changes can't land in one PR; types drift | Same repo keeps the contract in sync |
| Embed `dist/` in the Go binary | One deployable | Couples UI releases to the Go image; Node in the Go Dockerfile | Static-site component is simpler |
| Separate domain + CORS | Independent hosting | Needs CORS in the backend and a new TDR; cookies/CSRF more complex later | Same origin needs no backend change |

## Consequences
- Positive: typed client that breaks the build when the API contract changes; no backend change; one domain.
- Negative / trade-offs: Node toolchain joins the repo and CI; App Platform route change is a production change done once, with the owner's go-ahead.
- Follow-ups: a native app would reuse the API, not this code.

## Revisit When
- A mobile app is planned (consider React Native sharing `lib/`), or the UI needs SSR/SEO.
- The UI must be served from another domain (then a CORS TDR is needed).

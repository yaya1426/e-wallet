# E-Wallet web app

React + TypeScript single-page app for wallet users (PRD-005). Stack and layout: TDR-019;
sessions and security: TDR-020.

## Run it locally
```sh
make up            # postgres + wallet API (:8080) + bank simulator
make web-install   # npm ci (Node 24, see .nvmrc)
make web-dev       # http://localhost:5173, proxies /v1 and /internal to :8080
```
Set `WALLET_API_URL` to point the dev/preview proxy at another API.

## Checks
| Command | What |
|---------|------|
| `make web-gen-api` | Regenerate `src/api/schema.d.ts` after changing `api/openapi/wallet.yaml` |
| `make web-ci` | Generated-types check, ESLint, `tsc`, Vitest, production build (+ CSP check) |
| `make web-e2e` | Playwright against `vite preview` + the running stack |
| `WEB_BASE_URL=https://… npx playwright test e2e/smoke.spec.ts` | Smoke test a deployed app |

## Layout
`src/api` typed client · `src/auth` session (TDR-020) · `src/features/<area>` screens (never import
another feature) · `src/components` shared UI · `src/lib` money, phone, validation · `src/i18n/en.ts`
every user-facing string and the API error messages (`en.errors`, keyed by error code).

Money is integer piasters everywhere: format with `formatMoney`, parse input with `parseAmount`.

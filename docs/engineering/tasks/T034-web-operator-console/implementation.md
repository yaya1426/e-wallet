# T034 — Implementation

## Summary
An operator console under `/ops`, loaded as its own chunk, for the `/internal/v1` API: ops
health, discrepancies (list, detail, resolve), wallet freeze/unfreeze, and password-reset codes.
It uses the operator key as TDR-022 says and never touches the user session. No Go, API or
migration changes.

## Files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | `/ops/*` with route `lazy` (outside `RequireAuth`) |
| `web/src/api/opsClient.ts` | Client that adds `X-Internal-Api-Key` to `/internal/` only, strips `Authorization`, counts non-GET calls as activity, locks on 401 |
| `web/src/features/ops/opsKey.ts` | `OpsKeyStore`: sessionStorage `ewallet.ops.v1` = `{key, lastActive}`; idle window enforced on every read and by a timer |
| `web/src/features/ops/{OpsRoutes,OpsGate,OpsLayout,context}.tsx` | Provider, unlock screen, frame with nav and Lock, `noindex` meta |
| `web/src/features/ops/{OpsHealthPage,DiscrepanciesPage,DiscrepancyDetailPage,WalletsPage,ResetCodePage}.tsx` | Screens per UI-BR-32..35 |
| `web/src/features/ops/{queries.ts,opsErrors.tsx}` | Query keys under `['ops', …]`; operator error wording |
| `web/src/components/Button.tsx` | `danger` variant (Freeze) |
| `web/src/lib/validation.ts` | `isUuid`, `validateResolutionNote` |
| `web/src/i18n/en.ts` | `en.ops.*`, `en.opsErrors` |
| `web/scripts/check-dist.ts` | AC-11: no startup chunk (entry + modulepreload) contains `X-Internal-Api-Key`, and some chunk does |
| `web/e2e/ops.spec.ts`, `web/e2e/db.ts` | Browser tests; `db.ts` seeds and removes a discrepancy through `docker compose exec postgres psql` (skipped when that is not available) |

## Notes and deviations
- Component tests are in one `OpsConsole.test.tsx` (plus `opsKey.test.ts`, `opsClient.test.ts`)
  instead of one file per page; the bundle check extends the existing `check-dist.ts` instead of a
  new script; the seed helper is `e2e/db.ts` (`e2e/helpers.ts` is already a file). task.md updated.
- Activity (TDR-022 §3): clicks and key presses in the console and any non-GET call. Reads,
  including the 30 s health refresh, don't count. The timer keeps running after leaving `/ops`, so
  the key is removed on time; a reload after the window also finds it expired.
- On lock, all `['ops']` queries and mutations are removed from memory; the reset-code and
  wallet mutations also have `gcTime: 0`, so a code is gone once the screen is left.
- Detail without a GET-by-id API (Q4): the newest copy from any loaded list, else the full list is
  paged (at most 20 pages of 50). A non-UUID ID is "not found" without a request. A 409 on resolve
  keeps "Someone already resolved this discrepancy." on screen and reloads the item.

## AC → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `opsKey.test.ts`; `opsClient.test.ts` "a GET … leaves the idle window alone" | ✅ |
| AC-2 | `opsClient.test.ts` | ✅ |
| AC-3 | `OpsConsole.test.tsx` "a right key…", "a wrong key…"; e2e "unlock" | ✅ |
| AC-4 | `OpsConsole.test.tsx` "404 on unlock" | ✅ |
| AC-5 | `OpsConsole.test.tsx` "lists open items…resolves"; e2e "resolve a seeded discrepancy" | ✅ |
| AC-6 | `OpsConsole.test.tsx` "409 ALREADY_RESOLVED reloads the item…" | ✅ |
| AC-7 | e2e "freeze shows the banner to the user; unfreeze removes it" | ✅ |
| AC-8 | `OpsConsole.test.tsx` "invalid UUID…"; same e2e | ✅ |
| AC-9 | e2e "reset code end to end; unknown phone is refused"; component reset tests | ✅ |
| AC-10 | `OpsConsole.test.tsx` "details with <script> render as text"; e2e checks `<b>` stays literal | ✅ |
| AC-11 | `scripts/check-dist.ts` in `npm run build` | ✅ |
| AC-12 | `OpsConsole.test.tsx` "Lock returns…"; e2e "unlock" (new tab, reload) | ✅ |
| AC-13 | `OpsConsole.test.tsx` "refreshes every 30 s…", "a failed refresh…" | ✅ |
| AC-14 | `OpsConsole.test.tsx` "nav and every form work from the keyboard", status pill text | ✅ |
| AC-15 | `make web-ci`, `make web-e2e` locally; CI on the PR | ✅ locally |

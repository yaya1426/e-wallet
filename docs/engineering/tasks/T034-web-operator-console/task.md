# T034 — Web: operator console

| Field        | Value |
|--------------|-------|
| ID           | T034 |
| Status       | Done |
| Created      | 2026-11-12 |
| Branch       | `task/T034-web-operator-console` |
| Depends on   | T030, T031 (shared components, datetime); T029 only for the optional `USER_DISABLED` message |
| PRDs         | PRD-005 §5.6 (UI-BR-31..35); PRD-001 BR-8 (freeze) |
| TDRs         | TDR-022 (new), TDR-018 §4–5, TDR-016, TDR-019, TDR-020 |
| Docs changed | PRD-005 §5.6 (UI-BR-31..35), TDR-022 (new), docs/README.md |

## What
PRD-005 area 5: an operator console under `/ops` for the `/internal/v1` API: ops health,
discrepancies (list, detail, resolve), wallet freeze/unfreeze, and password-reset codes.

Deliverables:
- `features/ops/` lazily loaded route tree: `OpsGate` (unlock), `OpsLayout` (nav + Lock),
  `OpsHealthPage`, `DiscrepanciesPage`, `DiscrepancyDetailPage`, `WalletsPage`, `ResetCodePage`.
- `api/opsClient.ts` and `ops/opsKey.ts` per TDR-022 (sessionStorage, idle lock, 401 lock).
- Strings, unit, component and Playwright tests.

## Why
PRD-005 UI-BR-31..35. Operators currently use curl with the key (deployed test sheet); the
console makes incident handling and password-reset support usable without the terminal.

## How
### Modules and files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | `/ops/*` → route `lazy` import of `features/ops/OpsRoutes`; not wrapped in `RequireAuth` (separate credential). |
| `web/src/api/opsClient.ts` | `createClient<paths>({baseUrl:'/'})` + middleware adding `X-Internal-Api-Key` from `opsKey.get()`; on 401 → `opsKey.clear('invalid')`. Never adds `Authorization`. Errors parsed with T030 `errors.ts`. |
| `web/src/features/ops/opsKey.ts` | `OpsKeyStore`: `get/set/clear/touch/start/stop/subscribe`, `sessionStorage['ewallet.ops.v1']`, idle timer 15 min reset by `touch()`; injectable storage/clock for tests. |
| `web/src/features/ops/{OpsRoutes,OpsGate,OpsLayout}.tsx` | Gate shows unlock form when no key; layout nav: Health, Discrepancies, Wallets, Reset codes; Lock button; `touch()` on click/keydown. |
| `web/src/features/ops/OpsHealthPage.tsx` | `GET /internal/v1/health/ops`, `refetchInterval: 30000` (does not `touch()`). |
| `web/src/features/ops/DiscrepanciesPage.tsx`, `DiscrepancyDetailPage.tsx` | List with filters in URL search params (`?status=OPEN&kind=`), `useInfiniteQuery` limit 50; detail is read from the list cache or found by paging (no GET-by-id API — see notes); resolve form. |
| `web/src/features/ops/WalletsPage.tsx` | Wallet ID form, Freeze / Unfreeze with confirmation. |
| `web/src/features/ops/ResetCodePage.tsx` | Phone form, confirmation, code display. |
| `web/src/lib/validation.ts` | `isUuid`, `validateResolutionNote` (1–500 after trim). |
| `web/src/i18n/en.ts` | `en.ops.*` strings; `en.opsErrors` for internal codes (`UNAUTHENTICATED` "Wrong operator key.", `WALLET_NOT_FOUND`, `USER_NOT_FOUND`, `DISCREPANCY_NOT_FOUND`, `ALREADY_RESOLVED`, `INVALID_NOTE`, `INVALID_FILTER`, `USER_DISABLED` ops wording). |
| `web/e2e/ops.spec.ts`, `web/e2e/db.ts` | E2E below; `db.ts` seeds a discrepancy with `docker compose exec -T postgres psql` (local/CI only; the test is skipped when `WEB_BASE_URL` points at a remote server). |

### Data (migrations)
None.

### API
None changed. Used: `GET /internal/v1/health/ops`, `GET /internal/v1/discrepancies`,
`POST /internal/v1/discrepancies/{id}/resolve`, `POST /internal/v1/wallets/{id}/freeze|unfreeze`,
`POST /internal/v1/users/{phone}/password-reset-code` (phone path-escaped).

### Behaviour and design notes
- **Unlock (UI-BR-31):** password-type field "Operator key", `autocomplete="off"`. Submit calls
  health with the candidate key **before** storing it: 200 → store + go to Health; 401 → "Wrong
  operator key."; 404 → "The operator API is not enabled on this server."; network → connection
  message. Lock and idle timeout return here with an info message ("Console locked." /
  "Locked after 15 minutes without activity."). A 401 later → "The operator key was rejected.
  Unlock again."
- **Health (UI-BR-32):** status pill with text (not colour only): ok "All good", warning "Needs
  attention", critical "Critical". Cards for discrepancies, stuck (TOPUP/WITHDRAWAL), outbox,
  inbox; jobs table with `late` shown as "Late" badge; "Last checked {time}" + Refresh button.
  Counts > 0 for discrepancies link to the filtered list.
- **Discrepancies (UI-BR-34):** filters: status (Open/Resolved/All), kind (all enum values).
  `INVALID_FILTER` cannot happen from the UI's fixed options. Rows link to
  `/ops/discrepancies/:id`. Since there is no GET-by-id endpoint, the detail page uses the item
  from the list cache; on a direct load it pages through `status=` (all) until found (max 20
  pages), else "Discrepancy not found." `details` is rendered with `JSON.stringify(details,
  null, 2)` inside `<pre>` (text node, no HTML). Resolve: textarea note, client check, confirm
  dialog, POST; 200 → update caches, show resolved info; 409 → refetch + message; 404 → not found.
  Resolve never changes money; the dialog says so ("This only records a note; no money moves.").
- **Wallets (UI-BR-33):** wallet ID input validated as UUID; Freeze (danger style) and Unfreeze;
  confirmation "Freeze wallet {id}? The user can't top up, withdraw or send until it is unfrozen."
  / "Unfreeze wallet {id}?"; result "Wallet {id} is now FROZEN/ACTIVE". 404 → "No wallet with this ID."
- **Reset codes (UI-BR-35):** phone via `normalizeEgyptianMobile`; confirm; 201 → code in a large
  monospace block (`aria-label` spelled digits), copy button, "Expires at {time} (Cairo)"; code
  state lives only in the component and is gone on navigation. 404 `USER_NOT_FOUND`; 403
  `USER_DISABLED` (after T029) → ops wording; 400 `INVALID_PHONE`.
- Ops pages set `<meta name="robots" content="noindex">` via document head update on mount.
- The user session is not touched by any ops action, and vice versa.

### Out of scope
Per-operator accounts, audit log view, wallet search by phone or status read (no API), editing
money, viewing raw outbox/inbox events.

## Acceptance Criteria
- [x] AC-1 — `opsKey`: set stores in sessionStorage only; clear removes it; idle 15 min (fake clock) clears with reason `idle`; auto-refresh does not reset the idle timer; user activity does (unit).
- [x] AC-2 — `opsClient` sends `X-Internal-Api-Key` and no `Authorization`; the user client never sends the key; a 401 clears the key (unit).
- [x] AC-3 — Unlock with the compose key opens Health showing status and the jobs table; a wrong key shows "Wrong operator key." and stores nothing (e2e + component).
- [x] AC-4 — 404 from health on unlock shows "The operator API is not enabled on this server." (component).
- [x] AC-5 — Given a seeded OPEN discrepancy, it appears in the Open list; the detail shows its details JSON; resolving with a note moves it to Resolved with the note shown; an empty note is rejected with no request (e2e + component).
- [x] AC-6 — Resolving an already resolved item shows "Someone already resolved this discrepancy." (component with 409).
- [x] AC-7 — Given a user's wallet ID (read from their Home in the e2e), Freeze → the user's Home shows the frozen banner after reload; Unfreeze → banner gone (e2e across two browser contexts).
- [x] AC-8 — Invalid UUID rejected in the form; unknown UUID → "No wallet with this ID." (component + e2e).
- [x] AC-9 — Reset code for a registered phone (typed `010…`) shows a 6-digit code and expiry; the user can reset their password with it on `/reset-password` (e2e). Unknown phone → "No user with this phone number." (e2e).
- [x] AC-10 — Discrepancy `details` containing `<script>` text renders as text (component).
- [x] AC-11 — The ops chunk is not part of the main bundle (build check: main entry chunk does not contain the string `X-Internal-Api-Key`) (unit run after build in `web-build`).
- [x] AC-12 — Lock button returns to the unlock screen and clears sessionStorage; a new tab has to unlock again (component + e2e).
- [x] AC-13 — Health refreshes every 30 s (fake timers) and the Refresh button works (component).
- [x] AC-14 — Keyboard-only use of all ops forms; status pill readable without colour (component).
- [x] AC-15 — `make web-lint web-test web-build web-e2e` pass; CI green.

## Test Plan
| AC | Type | Test |
|----|------|------|
| 1 | unit | `features/ops/opsKey.test.ts` |
| 2 | unit | `api/opsClient.test.ts` |
| 3, 4 | component + e2e | `OpsConsole.test.tsx` (unlock); `e2e/ops.spec.ts` "unlock" |
| 5, 6, 10 | component + e2e | `OpsConsole.test.tsx` (discrepancies); e2e "resolve a seeded discrepancy" |
| 7, 8 | component + e2e | `OpsConsole.test.tsx` (wallets); e2e "freeze shows the banner to the user" |
| 9 | e2e | e2e "reset code end to end" |
| 11 | build check | `scripts/check-dist.ts` (runs in `npm run build`) |
| 12 | component + e2e | `OpsConsole.test.tsx` "Lock returns…"; e2e "unlock…" (also checks a new tab and reload) |
| 13, 14 | component | `OpsConsole.test.tsx` (health, keyboard) |
| 15 | CI | PR checks |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| Q1 | Where does the browser keep the operator key? | sessionStorage + 15-min idle lock / memory only / localStorage / backend operator login | Answered | sessionStorage + idle lock (TDR-022) |
| Q2 | How does an operator find a wallet ID to freeze? | User reads it from Home (T031 UI-BR-15) / new backend lookup by phone | Answered | From Home; backend lookup is a follow-up |
| Q3 | Same app or separate ops app? | Same app, lazy `/ops` chunk / separate deployable | Answered | Same app, lazy chunk |
| Q4 | Discrepancy detail without a GET-by-id API | Use list cache, page to find on direct load / add backend endpoint | Answered | List cache + paging |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Q1–Q4 recommended options | Claude as PM under Yahya's standing mandate (2026-11-12) | TDR-022 (new), PRD-005 §5.6, docs/README.md |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Ready | Planned; implementation after T031 (can run in parallel with T032/T033) |
| 2026-11-12 | In Review | Implemented; review round 1 (review.md) |
| 2026-11-12 | Done | Review fixes applied; checks pass locally |

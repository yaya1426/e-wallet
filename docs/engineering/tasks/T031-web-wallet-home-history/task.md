# T031 — Web: wallet home and transaction history

| Field        | Value |
|--------------|-------|
| ID           | T031 |
| Status       | Done |
| Created      | 2026-11-12 |
| Branch       | `task/T031-web-wallet-home-history` |
| Depends on   | T030 (web foundation + auth) |
| PRDs         | PRD-005 §5.1, §5.3 (UI-BR-13..19); PRD-001 BR-8/BR-9; PRD-004 P2P-BR-5 |
| TDRs         | TDR-019, TDR-020, TDR-013 (cursor pagination) |
| Docs changed | PRD-005 §5.3 (new rules UI-BR-13..19) |

## What
Replace T030's home placeholder with the real wallet home, and add the transaction history and
transaction detail screens (PRD-005 area 2). Also add the shared app navigation the later areas
plug into.

Deliverables:
- **App shell** for signed-in pages: header with app name, nav links (Home, History, Bank account,
  Send) and Sign out; on < 640 px the nav becomes a bottom tab bar. Routes for areas 3–4 render a
  "Coming soon" placeholder until their tasks land (removed by T032/T033).
- **Home** (`/`): balance card, frozen banner, quick actions, 5 newest transactions, Account
  section with copyable wallet ID.
- **History** (`/history`): paginated list with "Load more".
- **Transaction detail** (`/transactions/:id`).
- Shared: `TransactionRow`, `StatusBadge`, `Money` (signed amount), `CopyButton`, `formatDateTime`
  (Africa/Cairo), `transactionTitle`, `failureMessage`; hooks `useWallet`, `useTransactions`,
  `useTransaction`.
- Unit, component and Playwright tests.

## Why
PRD-005 UI-G1 (every wallet flow from a browser) and UI-BR-13..19. The wallet ID on home
(UI-BR-15) is how support finds the wallet to freeze in the operator console (T034), since the
API has no lookup by phone.

## How
### Modules and files
(Updated after implementation; see implementation.md "Deviations".)

| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | Signed-in routes nested under `RequireAuth` + `AppShell`: `/`, `/history`, `/transactions/:id`; placeholders `/bank-account`, `/top-up`, `/withdraw`, `/send` (`app/ComingSoonPage.tsx`, removed by T032/T033). |
| `web/src/components/AppShell.tsx` | Header + nav + `<Outlet/>`; bottom tab bar under 640 px; active link has `aria-current="page"` plus weight/underline; Send disabled on a frozen wallet. Also exports `Page` and `Card`. |
| `web/src/api/queries.ts` | Shared hooks (used by T032–T034 too): `useWallet()` (key `['me','wallet']`, as in T030), `useRecentTransactions()` (limit 5), `useTransactionList()` (infinite, 20/page), `useTransaction(id, {pollEveryMs, pollForMs})`; `queryKeys`, `invalidateMoney(queryClient)`; `usePendingPoll` (UI-BR-19); `useRefreshWalletWhenSettled` (refetch the wallet when a shown item goes from pending to final). |
| `web/src/features/home/{HomePage,HistoryPage,TransactionDetailPage}.tsx` | Screens; replaces `HomePlaceholder.tsx` (deleted). |
| `web/src/components/{TransactionRow,StatusBadge,CopyButton,ErrorWithRetry,DisabledAction}.tsx` | Shared, reused by T032–T034. |
| `web/src/lib/datetime.ts` | `formatDateTime(iso)` → `12 Nov 2026, 14:05` in `Africa/Cairo`. |
| `web/src/lib/transactions.ts` | `transactionTitle`, `typeLabel`, `statusLabel`, `isPending`, `isFinal`, `signedAmount`, `failureMessage`. |
| `web/src/lib/phone.ts` | `formatPhone('+201001234567')` → `+20 100 123 4567`. |
| `web/src/i18n/en.ts` | Strings for this area, `en.bankFailures`, errors `TRANSACTION_NOT_FOUND`, `WALLET_FROZEN`. |
| `web/e2e/walletApi.ts`, `web/e2e/home.spec.ts` | API setup helpers (users, bank link, top-up, P2P, freeze, `openAs`) and the browser tests. |

### Data (migrations)
None.

### API
None changed. Used: `GET /v1/me/wallet`, `GET /v1/me/transactions`, `GET /v1/transactions/{id}`.

### Behaviour and design notes
- **Balance card:** "Available balance" + `formatMoney(available_balance_minor)`; loading skeleton;
  on error an `Alert` with `messageForError` and Retry.
- **Frozen (UI-BR-14):** `status === 'FROZEN'` → `FrozenBanner` (`role="status"`), quick actions
  rendered as disabled buttons with `aria-disabled` and the reason as description.
- **Recent list:** 5 newest; empty → "No transactions yet."; "See all" → `/history`.
- **Row (UI-BR-16):** title via `transactionTitle` (`TOPUP` → "Top-up", `WITHDRAWAL` →
  "Withdrawal", `P2P`+`OUT` → "Sent to {masked}", `P2P`+`IN` → "Received from {masked}"),
  `+`/`−` (U+2212) + `formatMoney`, `StatusBadge` (PENDING/PROCESSING → "Pending", COMPLETED →
  "Completed", FAILED → "Failed"), date. FAILED amount in `<s>` with visually-hidden "(no money
  moved)". Whole row is a link to `/transactions/:id`.
- **History:** pages of 20; "Load more" shown while `next_cursor !== null`, disabled while
  fetching; a page error shows an Alert with Retry keeping already loaded rows. `INVALID_CURSOR`
  (should not happen) restarts from the first page.
- **Detail (UI-BR-18):** definition list of fields; P2P shows counterparty masked name and phone
  formatted `+20 100 123 4567`; note shown if non-null; completed time only when non-null;
  FAILED shows `failureMessage(failure_code)` (unknown code → generic + code) and, for
  `WITHDRAWAL`, "The money is back in your wallet." 404 → "Transaction not found." + link.
- **Polling (UI-BR-19):** `refetchInterval` returns 5000 while any shown item is PENDING or
  PROCESSING and less than 120 s have passed since the screen mounted, else `false`.
- **Phone display:** `formatPhone('+201001234567')` → `+20 100 123 4567` (added to `lib/phone.ts`).
- Money stays integer; `formatMoney` from T030 only.

### Out of scope
Top-up/withdrawal/bank screens (T032), send money (T033), operator console (T034), filters or
search in history, CSV export.

## Acceptance Criteria
- [x] AC-1 — Given a signed-in user with balance 150.75 EGP, when Home loads, then it shows `EGP 150.75` and the wallet ID with a working copy button (component: clipboard mocked).
- [x] AC-2 — Given wallet status FROZEN, then the frozen banner shows and Top up / Withdraw / Send are disabled (component).
- [x] AC-3 — Given no transactions, Home and History show "No transactions yet." (component).
- [x] AC-4 — Given items of every type/direction/status, rows show the right title, sign, badge, struck-through failed amount and Cairo date (unit for helpers, component for row). `formatDateTime('2026-11-12T12:05:00Z')` → `12 Nov 2026, 14:05`.
- [x] AC-5 — Given 45 transactions, History shows 20, then 40, then 45 after two "Load more" clicks, then hides the button (component with mocked pages; e2e with 25 real transactions → 20 then 25).
- [x] AC-6 — Given a failed withdrawal with `ACCOUNT_CLOSED`, the detail page shows "The bank account is closed." and "The money is back in your wallet."; unknown code shows the generic text with the code (component).
- [x] AC-7 — Given an unknown or foreign transaction id, the detail page shows "Transaction not found." (component + e2e with a random UUID).
- [x] AC-8 — Given a PENDING item on screen, the screen polls every 5 s and stops when it is COMPLETED or after 2 min (component with fake timers).
- [x] AC-9 — Given a real P2P between two users (set up through the API), each sees "Sent to …" / "Received from …" with the other's masked name (e2e).
- [x] AC-10 — Given a top-up done through the API with the success IBAN, after the bank webhook Home shows the new balance after focus/refetch (e2e).
- [x] AC-11 — Navigation works with keyboard; active link has `aria-current`; layout has no horizontal scroll at 360 px (component + e2e mobile project screenshot check).
- [x] AC-12 — `make web-lint web-test web-build web-e2e` pass; CI `web` and `web-e2e` green.

## Test Plan
| AC | Type | Test |
|----|------|------|
| 1, 2, 3 | component | `HomePage.test.tsx` |
| 4 | unit + component | `lib/datetime.test.ts`, `lib/transactions.test.ts`, `TransactionRow.test.tsx` |
| 5 | component + e2e | `HistoryPage.test.tsx` "load more"; `e2e/home.spec.ts` "history pagination" |
| 6, 7 | component + e2e | `TransactionDetailPage.test.tsx`; e2e "unknown transaction" |
| 8 | component | `HomePage.test.tsx` "polls pending" (vi fake timers) |
| 9, 10 | e2e | `e2e/home.spec.ts` |
| 11 | component + e2e | `AppShell.test.tsx`; e2e mobile project `scrollWidth <= innerWidth` |
| 12 | CI | PR checks |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| Q1 | Which time zone for dates? | Africa/Cairo (same as daily-limit day) / browser local | Answered | Africa/Cairo |
| Q2 | Show the wallet ID to users? | Yes, copyable in an Account section (support uses it to freeze) / no | Answered | Yes |
| Q3 | History paging style? | "Load more" button / infinite scroll / numbered pages | Answered | "Load more" (cursor API, accessible) |
| Q4 | Live updates for pending items? | Poll 5 s up to 2 min + refetch on focus / websockets (backend change) | Answered | Polling |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Q1–Q4 recommended options | Claude as PM under Yahya's standing mandate (2026-11-12: "take recommended options unless Yahya objects") | PRD-005 §5.3 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Ready | Planned; implementation starts after T030 is merged |
| 2026-11-12 | In Progress | Built on top of the T030 branch (PR #34, not yet merged); rebased onto main before the PR |
| 2026-11-12 | In Review | Implemented and verified; review round 1: 1 Major + 12 Minor/Nit, all fixed; round 2 Approved |
| 2026-11-12 | Done | Finalized — PR opened (auto-merge) |

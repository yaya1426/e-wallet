# T031 — Implementation

## Summary
The web app's signed-in area now has a shared shell (header nav, bottom tab bar under 640 px,
Sign out), the real wallet **Home** (balance, frozen banner, quick actions, 5 newest
transactions, copyable wallet ID), **History** (20 per page, "Load more" over `next_cursor`)
and **Transaction detail** (`/transactions/:id`). Routes for areas 3–4 show a "Coming soon"
placeholder until T032/T033. No Go code, API or migration changes.

## Files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | Signed-in routes nested under `RequireAuth` + `AppShell`; placeholders for `/bank-account`, `/top-up`, `/withdraw`, `/send` |
| `web/src/app/ComingSoonPage.tsx` | Placeholder page (removed by T032/T033) |
| `web/src/api/queries.ts` | Shared hooks `useWallet`, `useRecentTransactions`, `useTransactionList`, `useTransaction`; `queryKeys`, `invalidateMoney`, `usePendingPoll` (UI-BR-19) |
| `web/src/components/{AppShell,TransactionRow,StatusBadge,CopyButton,ErrorWithRetry,DisabledAction}.tsx` | Shared UI |
| `web/src/features/home/{HomePage,HistoryPage,TransactionDetailPage}.tsx` | Screens; `HomePlaceholder` removed |
| `web/src/lib/{datetime,transactions}.ts`, `lib/phone.ts` (`formatPhone`) | Display helpers |
| `web/src/i18n/en.ts` | Strings for home/nav/transactions, `bankFailures`, errors `TRANSACTION_NOT_FOUND`, `WALLET_FROZEN` |
| `web/src/test/fixtures.ts` | Wallet/transaction fixtures |
| `web/e2e/walletApi.ts` | E2E setup through the API: users, bank link, top-up (waits for the bank), P2P, freeze; `openAs` seeds the stored refresh token so the UI restores the session without spending a login |
| `web/e2e/home.spec.ts` | Browser tests for this task |
| Docs | PRD-005 §5.3–5.6 (UI-BR-21 aligned with T030 `parseAmount`), TDR-022, task folders T031–T034, index rows |

## Deviations from the plan
- The query hooks live in `web/src/api/queries.ts` instead of `features/home/hooks.ts`: T032–T034
  need `useWallet`/`useTransaction`, and TDR-019 §2 forbids one feature importing another.
  `lib/queryKeys.ts` was merged into the same file. The wallet query key stays `['me','wallet']`
  as T030 used it.
- `Money` and `Spinner`/`EmptyState` components were not needed as separate files: amounts use
  `signedAmount`/`formatMoney`, T030's `Spinner` is reused, the empty state is one line of text.
- The polling window (2 min) starts when a pending item is first seen, not at mount (lint rule
  `react-hooks/purity` forbids reading the clock during render). The effect is the same.
- After review: `useRefreshWalletWhenSettled` refetches the balance when a shown transaction
  settles, and the frozen state also disables Send in the navigation (review.md).
- E2E signs users in by seeding the refresh token (`openAs`) instead of the login form, because
  the login limit (30/min per IP, T004) is shared by every test; UI login itself stays covered by
  T030's `auth.spec.ts`. API logins retry on 429.

## AC → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `HomePage.test.tsx` "shows the balance… copyable wallet ID"; e2e "a top-up shows on home…" | ✅ |
| AC-2 | `HomePage.test.tsx` "frozen wallet"; e2e "frozen wallet shows the banner…" | ✅ |
| AC-3 | `HomePage.test.tsx` / `HistoryPage.test.tsx` "empty history" | ✅ |
| AC-4 | `lib/datetime.test.ts`, `lib/transactions.test.ts`, `TransactionRow.test.tsx`, `AppShell.test.tsx` | ✅ |
| AC-5 | `HistoryPage.test.tsx` "loads 20, then 40, then 45", "a failed page keeps the loaded rows"; e2e "history pages 20 at a time" | ✅ |
| AC-6 | `TransactionDetailPage.test.tsx` "failed withdrawal…", "unknown failure code…" | ✅ |
| AC-7 | `TransactionDetailPage.test.tsx` "404…"; e2e "an unknown transaction…" | ✅ |
| AC-8 | `HomePage.test.tsx` "polls while…", "stops polling… after 2 minutes", "new pending item… own window", "refetches the balance when… completes" | ✅ |
| AC-9 | e2e "a P2P transfer shows on both sides with masked names" | ✅ |
| AC-10 | e2e "a top-up shows on home with the new balance" (refetch on tab focus, no reload) | ✅ |
| AC-11 | `AppShell.test.tsx` "marks the active link…"; e2e "no horizontal scroll at phone width" (desktop + mobile) | ✅ |
| AC-12 | `make web-ci`, `make web-e2e` below; CI on the PR | ✅ locally |

## Verification
| Step | Command | Result |
|------|---------|--------|
| API types up to date | `make web-gen-check` | pass |
| ESLint + tsc | `make web-lint` | pass, 0 problems |
| Unit + component | `make web-test` | pass, 16 files / 175 tests |
| Build + CSP/inline check | `make web-build` | pass, `check-dist: CSP present, no inline scripts or styles` |
| Browser tests vs compose stack | `make web-e2e` (desktop + 390×844 mobile) | 23/23 pass |
| Go build (no Go changes) | `go build ./...` | pass |
| Visual check | Screenshots of Home at 1100 px and 390 px | Layout correct, no overflow |

## Follow-ups
- None required. A later backend task could add a wallet lookup by phone for operators (T034 Q2).

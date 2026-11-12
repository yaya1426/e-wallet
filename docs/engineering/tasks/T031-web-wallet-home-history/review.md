# Review — T031 (2026-11-12, rounds 1–2)

## Round 1 — Changes Requested
Independent read of every changed file against task.md, PRD-005 §5.1/§5.3, TDR-019/020.
Static state: `tsc -b --noEmit` clean, ESLint clean, Vitest 166 passing.

| # | Severity | File:line | Finding | Fix | Status |
|---|----------|-----------|---------|-----|--------|
| 1 | Major | `web/src/api/queries.ts` | Only the transaction list polled; when a pending top-up completed, the row said "Completed" while the balance stayed old until focus/reload (UI-BR-19). | `useRefreshWalletWhenSettled` invalidates the wallet when any shown item goes pending → final; test "refetches the balance when a pending transaction completes". | Fixed |
| 2 | Minor | `web/src/api/queries.ts` | 2-minute poll window never restarted, so a later pending item was never polled. | Window resets when nothing is pending; test "a new pending item … gets its own polling window". | Fixed |
| 3 | Minor | `web/src/features/home/HistoryPage.tsx` | `INVALID_CURSOR` not handled; Retry always called `fetchNextPage`. | Reset the list on `INVALID_CURSOR`; Retry picks `fetchNextPage` only for `isFetchNextPageError`; test added. | Fixed |
| 4 | Minor | `web/src/features/home/HomePage.tsx` | Frozen quick actions were unfocusable spans. | `DisabledAction`: focusable `button` with `aria-disabled` + description; test checks focus and description. | Fixed |
| 5 | Minor | `web/src/components/AppShell.tsx` | Nav "Send" active on a frozen wallet (UI-BR-14 "everywhere"). | Send rendered as `DisabledAction` when frozen; tested. | Fixed |
| 6 | Minor | `web/src/components/CopyButton.tsx` | Successful copy not announced. | Live region announces "Copied to the clipboard."; tested. | Fixed |
| 7 | Minor | `web/src/components/AppShell.tsx` | Active nav link differed by colour only. | Active link also bold + underlined. | Fixed |
| 8 | Minor | `web/e2e/home.spec.ts` | AC-10 used `reload()`, not focus refetch. | Dispatches `visibilitychange` (TanStack's focus signal) instead. | Fixed |
| 9 | Minor | `web/e2e/walletApi.ts` | 429 back-off (75 s) longer than the 30 s test timeout; contexts not closed. | Back-off capped at ~50 s, spec timeout 90 s, contexts closed. | Fixed |
| 10 | Minor | tests | No row-level test of every type/direction/status. | `TransactionRow.test.tsx` table test. | Fixed |
| 11 | Nit | `web/src/features/home/TransactionDetailPage.tsx` | No Type/Direction row; unused strings. | "Type: Transfer · Money in" row; unused strings removed. Home shows status only when FROZEN (banner) — accepted: an ACTIVE badge adds noise. | Fixed |
| 12 | Nit | `web/src/lib/transactions.ts` | Plain-object lookup (`'constructor'`). | `Object.hasOwn`. | Fixed |
| 13 | Nit | task.md How | File table differed from the code. | Table updated; deviations in implementation.md. | Fixed |

Checked and fine: integer-only money; Cairo dates incl. DST/midnight; routing/guards; feature
import rule; TanStack retry/4xx behaviour; alerts/live regions; no `dangerouslySetInnerHTML`;
cache cleared on sign-out; e2e assertions match the API's masked-name and phone formats.

## Round 2 — Approved
All 13 findings verified fixed in the code with tests. Re-verification: `make web-ci` pass,
175 unit/component tests, 23/23 browser tests (desktop + mobile) against the compose stack.
Verdict: **Approved**.

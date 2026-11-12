# Review — T034 (2026-11-12, rounds 1–2)

## Round 1 — Changes Requested
Independent read against task.md, TDR-022, PRD-005 §5.6 and the backend handlers. Static state:
tsc clean, 39 ops/api tests passing. No blocker.

| # | Severity | File | Finding | Fix | Status |
|---|----------|------|---------|-----|--------|
| 1 | Major | `DiscrepancyDetailPage.tsx` | After 409 the reloaded item is RESOLVED, the form unmounts and the "already resolved" message vanished; the test's fake never changed state. | Message kept at page level and focused; test makes the item RESOLVED after the 409 and checks both. | Fixed |
| 2 | Minor | `DiscrepancyDetailPage.tsx` | Detail cache (`staleTime: Infinity`) hid a newer list copy. | The newest of list copy and detail copy is shown (`dataUpdatedAt`). | Fixed |
| 3 | Minor | `ResetCodePage.tsx`, `OpsGate.tsx` | Reset code stayed in the mutation cache after leaving or locking. | `mutationKey: ['ops', …]`, `gcTime: 0`; lock removes ops mutations. | Fixed |
| 4 | Minor | `OpsRoutes.tsx` | Idle timer stopped when leaving `/ops`, so the key outlived 15 minutes. | Timer keeps running. | Fixed |
| 5 | Minor | `ResetCodePage.tsx` | `aria-label` on `<p>` is ignored by screen readers. | Visible digits `aria-hidden` + sr-only spelled digits. | Fixed |
| 6 | Minor | `DiscrepanciesPage.tsx` | Rows lacked the transaction ID (UI-BR-34). | Column added (short form, full ID in title). | Fixed |
| 7 | Minor | `OpsHealthPage.tsx` | Failed background refresh was silent; auto-refresh disabled the focused Refresh button. | "Couldn't refresh" alert; loading only for manual refresh. Test added. | Fixed |
| 8 | Minor | `opsClient.test.ts` | Nothing proved GET vs POST activity through the client. | Test added. | Fixed |
| 9 | Minor | `e2e/db.ts` | Skip only on `WEB_BASE_URL`; a failed run left an OPEN discrepancy. | Probes `docker compose ps`; seeded row deleted in `finally`. | Fixed |
| 10 | Nit | `en.ts` | Reset confirm and `USER_DISABLED` wording differed from the PRD. | PRD text used. | Fixed |
| 11 | Nit | `DiscrepancyDetailPage.tsx` | Non-UUID ID paged 20 times. | Short-circuit with `isUuid`. | Fixed |
| 12 | Nit | `check-dist.ts` | Only the entry script was checked. | `modulepreload` chunks checked too. | Fixed |
| 13 | Nit | `OpsHealthPage.tsx` | "Last checked" announced every 30 s. | `aria-live` removed. | Fixed |

Checked and fine: key only in sessionStorage, never in URL/logs/localStorage/user client;
`Authorization` stripped; 401 locks (not for the candidate key); idle lock on read and reload;
lock clears ops caches; error codes match the backend; phone path escaped as `%2B`; `details`
rendered as text; dialogs, focus, labels and keyboard path; lazy route outside `RequireAuth`.

## Round 2 — Approved
Fixes verified with tests. `make web-ci` passes (272 tests, bundle check), browser tests pass
against the compose stack. Verdict: **Approved**.

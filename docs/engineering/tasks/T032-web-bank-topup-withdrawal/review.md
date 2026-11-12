# Review — T032 (2026-11-12, rounds 1–2)

## Round 1 — Changes Requested
Independent read of every changed file against task.md, PRD-005 §5.1/§5.4, TDR-005/019/020 and
the server IBAN rules. Static state: tsc and ESLint clean, Vitest 215 passing.

| # | Severity | File | Finding | Fix | Status |
|---|----------|------|---------|-----|--------|
| 1 | Major | `features/money/MoneyFlowPage.tsx` | A failed background wallet refetch after the 202 replaced the status panel with a load error, so the user saw no trace of the accepted request and might send it again. | The status branch renders first; only a failed *first* load (no data) blocks the page. Test "a failed wallet refetch after 202 never hides…". | Fixed |
| 2 | Minor | `api/queries.ts` | `useTransaction` stopped polling for good if its first GET failed. | No data yet counts as pending inside the window. Test "keeps polling when the first status GET fails…". | Fixed |
| 3 | Minor | `components/TransactionStatusPanel.tsx` | Balance not refreshed when the first poll was already final. | Panel invalidates the wallet whenever the status is final. Same test. | Fixed |
| 4 | Minor | `MoneyFlowPage.tsx` | "Try again" stayed visible while the amount was edited and would resend the old amount. | Editing the amount clears the error. Test added. | Fixed |
| 5 | Minor | `lib/useIdempotentRequest.ts` | Held key lost when leaving the page after an ambiguous failure. | Key also kept in `sessionStorage` per flow until a final answer; tests added. | Fixed |
| 6 | Minor | `components/ConfirmDialog.tsx` | Focus returned to a disabled opener and fell to `<body>`. | Dialog takes `returnFocus`; errors and the status panel receive focus. Test added. | Fixed |
| 7 | Minor | `components/ConfirmDialog.tsx` | Trap/Escape only on the panel; background not inert. | Portal to `<body>`, everything else `inert`, Escape on `document`. Test added. | Fixed |
| 8 | Minor | `e2e/money.spec.ts` | AC-7 never checked the immediate reserve. | Asserts the API balance is 300 while the status is still pending. | Fixed |
| 9 | Nit | `lib/useIdempotentRequest.ts` | `submit` could swap the held key during a flight. | Early return before touching the key. | Fixed |
| 10 | Nit | `e2e/money.spec.ts` | AC-4 e2e only for top-up; AC-2 e2e didn't assert "no request". | Both added; AC-12 stale-submit 403 stays covered by the component test. | Fixed |
| 11 | Nit | `MoneyFlowPage.test.tsx` | Double-click test didn't reach the guard (dialog unmounted). | Dialog now stays open and busy until the answer, so the second click hits a disabled button. | Fixed |
| 12 | Nit | `lib/validation.ts` | JS `trim()` vs Go `TrimSpace` differ on U+FEFF/U+0085. | Not changed: server error is mapped to the field; impact negligible. | Won't fix |
| 13 | Nit | `en.ts`, task.md | Unused strings; task names differ from code. | Strings removed; task How/Test Plan updated. | Fixed |

Checked and fine: IBAN acceptance identical to the Go `NormalizeIBAN`; amounts are integer
piasters; key lifecycle matches UI-BR-24; error mapping per flow; status polling window; bank
account page states; React hooks rules; e2e scenarios match the simulator suffixes.

## Round 2 — Approved
All fixes verified with tests. Re-verification: `make web-ci` pass (222 tests), 32/32 browser
tests against the compose stack. Verdict: **Approved**.

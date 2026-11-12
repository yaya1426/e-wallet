# T032 — Implementation

## Summary
Users can now link or replace their bank account, top up from it and withdraw to it from the
web app. Each money action has a confirmation dialog, an `Idempotency-Key` that survives
retries of ambiguous failures, and a status panel that follows the transaction until the bank
answers (2 s polling for up to 60 s, then a "taking longer" note). No Go, API or migration
changes.

## Files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | Real `/bank-account`, `/top-up`, `/withdraw` routes |
| `web/src/features/money/hooks.ts` | `useBankAccount` (404 → `null`), `useLinkBankAccount`, `useSendMoney(kind)` |
| `web/src/features/money/BankAccountPage.tsx` | View + link/replace form, replace confirmation |
| `web/src/features/money/MoneyFlowPage.tsx` | Shared top-up/withdrawal flow (`TopUpPage`, `WithdrawPage`) |
| `web/src/components/{AmountField,ConfirmDialog,TransactionStatusPanel}.tsx` | Shared, reused by T033 |
| `web/src/components/Button.tsx` | Accepts `ref` (React 19 ref-as-prop) so the dialog can focus Confirm |
| `web/src/lib/iban.ts` | `normalizeIban` (spaces only, like the API), `isValidEgyptianIban` (regex + mod-97 in 7-digit chunks), `formatIban`, `ibanLast4` |
| `web/src/lib/useIdempotentRequest.ts` | Key lifecycle per UI-BR-24 (see notes) |
| `web/src/i18n/en.ts` | Strings for the area; error codes `INVALID_IBAN`, `INVALID_HOLDER_NAME`, `BANK_ACCOUNT_*`, `INVALID_AMOUNT`, `WALLET_LIMIT_EXCEEDED`, `INSUFFICIENT_FUNDS`, `DAILY_LIMIT_EXCEEDED`, `REQUEST_IN_PROGRESS`, `TRY_AGAIN` |
| `web/e2e/money.spec.ts`, `web/e2e/walletApi.ts` | Browser tests with the bank simulator scenarios; `openAs` now waits for the seeded token to rotate |

## Notes and deviations
- `useIdempotentMutation` was built as `useIdempotentRequest` (plain state, not TanStack
  `useMutation`), because the key must live across retries and Back/Confirm cycles:
  - Confirm → a new UUID v4 key unless a key is held for **exactly the same** request.
  - Network error, `409 REQUEST_IN_PROGRESS`, `503`, any 5xx → key held; "Try again" resends.
  - Back without changes, then Confirm → the held key is reused (the first request may have
    gone through). Changed amount → new key.
  - Success or any other 4xx → key dropped.
  - A second submit while one is in flight is ignored (double click = one request).
- The task said "Back resets the key". We keep the key when the user comes back with the same
  amount, which is stricter about double charges and still matches UI-BR-24 ("dropped when the
  user goes Back and **changes anything**").
- After review (review.md): the status screen can't be hidden by a failed background refetch,
  polling survives a failed first GET, the held key is also kept in `sessionStorage`, the dialog
  makes the page `inert` and stays open (busy) until the answer, and focus moves to the result.
- `TRY_AGAIN` now has its own message ("The server was busy. Please try again.") instead of the
  generic text, since the user can act on it.

## AC → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `lib/iban.test.ts` | ✅ |
| AC-2 | `BankAccountPage.test.tsx` "links a first account", "invalid IBAN and short name", "server INVALID_IBAN"; e2e "link an account…" | ✅ |
| AC-3 | `BankAccountPage.test.tsx` "replacing asks for confirmation" | ✅ |
| AC-4 | `MoneyFlowPage.test.tsx` "no linked account"; e2e "link an account…" | ✅ |
| AC-5 | `MoneyFlowPage.test.tsx` "top-up: confirm… to completion"; e2e "top-up succeeds" | ✅ |
| AC-6 | e2e "top-up the bank refuses later" | ✅ |
| AC-7 | e2e "withdrawal reserves at once and completes" | ✅ |
| AC-8 | `MoneyFlowPage.test.tsx` "withdrawal failure…"; e2e "withdrawal the bank refuses is refunded" | ✅ |
| AC-9 | `MoneyFlowPage.test.tsx` amount table + "sends piasters" (`1,250.5` → `125050`) | ✅ |
| AC-10 | `MoneyFlowPage.test.tsx` "network error → Try again…", "503 TRY_AGAIN…"; `useIdempotentRequest.test.ts` | ✅ |
| AC-11 | `MoneyFlowPage.test.tsx` 422 table; e2e "withdrawing more than the balance" | ✅ |
| AC-12 | `MoneyFlowPage.test.tsx` "frozen wallet…", "403 WALLET_FROZEN on submit…"; e2e "a frozen wallet cannot top up" | ✅ |
| AC-13 | `MoneyFlowPage.test.tsx` "no final state within 60 s" | ✅ |
| AC-14 | `MoneyFlowPage.test.tsx` "double-clicking Confirm…"; `useIdempotentRequest.test.ts` "in flight" | ✅ |
| AC-15 | `MoneyFlowPage.test.tsx` "the confirm dialog keeps Tab inside…"; `BankAccountPage.test.tsx` (Escape, focus) | ✅ |
| AC-16 | Verification below; CI on the PR | ✅ locally |

## Verification
| Step | Command | Result |
|------|---------|--------|
| API types up to date | `make web-gen-check` | pass |
| ESLint + tsc | `make web-lint` | pass |
| Unit + component | `make web-test` | pass, 222 tests |
| Build + CSP check | `make web-build` | pass |
| Browser tests vs compose stack + banksim | `make web-e2e` | 32/32 pass (desktop + mobile) |

## Follow-ups
- Reloading during the startup refresh can end the session through reuse detection (found while
  writing e2e; reported to the T030 owner; a fix needs a backend grace window).

# T032 — Web: bank account linking, top-up and withdrawal

| Field        | Value |
|--------------|-------|
| ID           | T032 |
| Status       | Done |
| Created      | 2026-11-12 |
| Branch       | `task/T032-web-bank-topup-withdrawal` |
| Depends on   | T030, T031 (app shell, transaction hooks/components) |
| PRDs         | PRD-005 §5.4 (UI-BR-20..26), UI-BR-14, UI-BR-18; PRD-002; PRD-003; PRD-001 §5.6 |
| TDRs         | TDR-005 (idempotency), TDR-017, TDR-019, TDR-020 |
| Docs changed | PRD-005 §5.4 (new rules UI-BR-20..26) |

## What
PRD-005 area 3: the user can link or replace a bank account, top up from it, withdraw to it,
and watch each request until the bank answers.

Deliverables:
- **Bank account** screen (`/bank-account`): view + link/replace form.
- **Top up** (`/top-up`) and **Withdraw** (`/withdraw`): amount → confirm → submit → status.
- Shared: `AmountField`, `ConfirmDialog` (accessible modal), `useIdempotentMutation`
  (key lifecycle per UI-BR-24), `TransactionStatusPanel` (polling per UI-BR-25), `lib/iban.ts`.
- Unit, component and Playwright tests using the bank simulator IBAN scenarios.

## Why
PRD-005 UI-G1/UI-G2, UI-BR-20..26; PRD-002 (top-up is async, credited on bank success);
PRD-003 (withdrawal reserves at once, refunded on failure); TDR-005 (one key per user action,
safe retries).

## How
### Modules and files
| Path | Change |
|------|--------|
| `web/src/app/router.tsx` | Real routes for `/bank-account`, `/top-up`, `/withdraw` (remove T031 placeholders). |
| `web/src/features/money/BankAccountPage.tsx` | `GET /v1/me/bank-account` (404 → empty state) + form; `PUT` on submit. |
| `web/src/features/money/MoneyFlowPage.tsx` | Shared flow for `kind: 'topup' │ 'withdrawal'` (amount, confirm, submit, status). `TopUpPage`/`WithdrawPage` are thin wrappers. |
| `web/src/features/money/hooks.ts` | `useBankAccount()` (key `['bankAccount']`), `useLinkBankAccount()`, `useTopUp()`, `useWithdraw()`. |
| `web/src/components/{AmountField,ConfirmDialog,TransactionStatusPanel}.tsx` | Shared, reused by T033. `ConfirmDialog` portals to `<body>`, makes the rest `inert`, stays open (busy) until the answer. |
| `web/src/lib/useIdempotentRequest.ts` | (Built instead of `useIdempotentMutation`.) `submit(vars)` reuses the held key only for identical vars, else a new UUID v4; retryable failures keep the key (also in `sessionStorage` per flow); success/other 4xx drop it; `retry()` resends; `reset()` clears the answer only. |
| `web/src/lib/iban.ts` | `normalizeIban`, `isValidEgyptianIban` (regex + mod-97 using chunked integer math, no BigInt needed), `formatIban` (groups of 4), `ibanLast4`. |
| `web/src/i18n/en.ts` | Strings; `en.errors` adds `INVALID_IBAN`, `INVALID_HOLDER_NAME`, `BANK_ACCOUNT_NOT_FOUND`, `BANK_ACCOUNT_REQUIRED`, `AMOUNT_OUT_OF_RANGE` (per flow), `WALLET_LIMIT_EXCEEDED`, `INSUFFICIENT_FUNDS`, `DAILY_LIMIT_EXCEEDED`, `REQUEST_IN_PROGRESS`, `IDEMPOTENCY_KEY_REUSED` (generic), `INVALID_AMOUNT`. |
| `web/e2e/money.spec.ts` | E2E below. |

### Data (migrations)
None.

### API
None changed. Used: `GET/PUT /v1/me/bank-account`, `POST /v1/topups`, `POST /v1/withdrawals`
(header `Idempotency-Key`), `GET /v1/transactions/{id}`, `GET /v1/me/wallet`.

### Behaviour and design notes
- **Bank account (UI-BR-20):** view card shows `formatIban(iban)`, holder, "Last updated
  {formatDateTime}". Button "Link bank account" / "Replace bank account" opens the form. Client
  validation on submit: IBAN → holder (show all errors). Replacing shows `ConfirmDialog` first.
  On 200: update `['bankAccount']` cache with the response, success Alert "Bank account saved.",
  close form. Server `INVALID_IBAN`/`INVALID_HOLDER_NAME` → field errors; others → form Alert.
- **Flow states:** `form → confirm → submitting → status` (+ `error` with Try again when
  retryable). Frozen wallet (from `useWallet`) → banner instead of the form (UI-BR-14). No
  bank account → "Link a bank account first." + link (UI-BR-22).
- **Amount (UI-BR-21):** `AmountField` uses `parseAmount`; messages per its error; `inputmode="decimal"`.
  For withdrawal the screen shows "Available: EGP …"; no client-side limit checks.
- **Confirm (UI-BR-23):** `ConfirmDialog` text with `formatMoney` and `ibanLast4`; focus trapped,
  Escape = Back.
- **Idempotency (UI-BR-24):** retryable = `NETWORK_ERROR`, `409 REQUEST_IN_PROGRESS`, `503 TRY_AGAIN`,
  any 5xx. Retryable errors show the message + "Try again" (same key). Non-retryable 4xx show the
  message and "Back" (resets the key). Back from confirm also resets it.
- **Status (UI-BR-25):** after 202 (`transaction_id`), invalidate wallet + transactions
  (`invalidateMoney`), show `TransactionStatusPanel`: `useTransaction(id)` with
  `refetchInterval` 2000 until COMPLETED/FAILED or 60 s; then the timeout text with a link to
  `/transactions/:id`. COMPLETED → "Top-up complete" / "Withdrawal complete" + new balance (wallet
  refetched); FAILED → `failureMessage` (+ refund line for withdrawals). A replay
  (`Idempotent-Replayed: true`) is treated the same as a fresh 202.
- **Errors:** `WALLET_FROZEN` refetches the wallet (banner appears). `BANK_ACCOUNT_REQUIRED`
  refetches the bank account.

### Out of scope
Removing a linked account (no API), multiple accounts, showing limit values, P2P (T033).

## Acceptance Criteria
- [x] AC-1 — `isValidEgyptianIban` accepts `EG890003000100000000123450000` and the same with spaces/lower case; rejects a wrong check digit, wrong length, non-EG; `formatIban` groups by 4 (unit).
- [x] AC-2 — Given no linked account, when the user links a valid IBAN, then the card shows it; invalid IBAN → field error and no request (component + e2e).
- [x] AC-3 — Given a linked account, replacing asks for confirmation and saves only after Confirm (component).
- [x] AC-4 — Given no linked account, Top up and Withdraw show "Link a bank account first." (component + e2e).
- [x] AC-5 — Given IBAN ending `0000`, when the user tops up EGP 500, then the status goes Pending → "Top-up complete" and Home shows +500 (e2e).
- [x] AC-6 — Given IBAN ending `0001`, a top-up ends FAILED with "Your bank account didn't have enough money." and the balance is unchanged (e2e).
- [x] AC-7 — Given balance 500 and IBAN `0000`, a withdrawal of 200 lowers the available balance at once and ends "Withdrawal complete" (e2e).
- [x] AC-8 — Given IBAN `0001`, a withdrawal ends FAILED with "The bank account is closed." + "The money is back in your wallet." and the balance is restored (e2e).
- [x] AC-9 — Amount input: `abc`, `1.234`, `0` show their errors and send nothing; `1,250.5` is sent as `125050` (component asserting the request body).
- [x] AC-10 — Given the first submit fails with a network error / 503, when the user presses Try again, then the second request carries the **same** `Idempotency-Key`; after Back + edit a **new** key is used (component with mocked fetch).
- [x] AC-11 — 422 `AMOUNT_OUT_OF_RANGE`, `INSUFFICIENT_FUNDS`, `DAILY_LIMIT_EXCEEDED`, `WALLET_LIMIT_EXCEEDED` show their messages (component); `INSUFFICIENT_FUNDS` also e2e (withdraw more than the balance).
- [x] AC-12 — Given a frozen wallet (frozen via the internal API in e2e setup), Top up shows the frozen banner; a stale form submit getting 403 `WALLET_FROZEN` shows the frozen message (component + e2e).
- [x] AC-13 — Given the bank never answers within 60 s, the status panel shows the "taking longer" text with a link (component with fake timers).
- [x] AC-14 — Confirm button is disabled while submitting (double-click sends one request) (component).
- [x] AC-15 — Dialogs trap focus, close on Escape, and every field has a label (component).
- [x] AC-16 — `make web-lint web-test web-build web-e2e` pass; CI green.

## Test Plan
| AC | Type | Test |
|----|------|------|
| 1 | unit | `lib/iban.test.ts`; `lib/useIdempotentRequest.test.ts` (key lifecycle) |
| 2, 3 | component + e2e | `BankAccountPage.test.tsx`; `e2e/money.spec.ts` "link account" |
| 4 | component + e2e | `MoneyFlowPage.test.tsx` "no bank account"; e2e |
| 5–8 | e2e | `e2e/money.spec.ts` scenario tests (IBAN suffixes 0000/0001) |
| 9, 10, 11, 13, 14 | component | `MoneyFlowPage.test.tsx`, `useIdempotentMutation.test.ts` |
| 12 | component + e2e | e2e uses `POST /internal/v1/wallets/{id}/freeze` with the compose key |
| 15 | component | `MoneyFlowPage.test.tsx` (dialog focus, Tab, Escape, inert), `BankAccountPage.test.tsx` |
| 16 | CI | PR checks |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| Q1 | Check min/max limits in the UI? | No, server config is the authority, map `AMOUNT_OUT_OF_RANGE` / hard-code PRD values | Answered | No client limit checks |
| Q2 | How long to poll after 202? | 2 s for 60 s then "taking longer" + history link / poll forever | Answered | 2 s for 60 s |
| Q3 | Idempotency key lifetime | New per Confirm, reused on retryable failure, dropped on edit/final answer / new key per click | Answered | As recommended |
| Q4 | Confirmation step before money moves? | Yes, dialog with amount and IBAN last 4 / none | Answered | Yes |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Q1–Q4 recommended options | Claude as PM under Yahya's standing mandate (2026-11-12) | PRD-005 §5.4 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Ready | Planned; implementation after T031 |
| 2026-11-12 | In Review | Implemented on top of T031; review round 1: 1 Major + 12 Minor/Nit, 12 fixed, 1 won't fix; round 2 Approved |
| 2026-11-12 | Done | Finalized — PR opened (auto-merge) |

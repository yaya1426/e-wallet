# T033 — Web: P2P transfer with recipient preview

| Field        | Value |
|--------------|-------|
| ID           | T033 |
| Status       | Done |
| Created      | 2026-11-12 |
| Branch       | `task/T033-web-p2p-transfer` |
| Depends on   | T030, T031, T032 (AmountField, ConfirmDialog, useIdempotentRequest) |
| PRDs         | PRD-005 §5.5 (UI-BR-27..30), UI-BR-21/24/26; PRD-004 (P2P-BR-5 masked name) |
| TDRs         | TDR-005, TDR-019, TDR-020 |
| Docs changed | PRD-005 §5.5 (new rules UI-BR-27..30) |

## What
PRD-005 area 4: "Send money" (`/send`) as a 3-step flow — recipient (with masked-name preview),
amount and note, confirm — then a result screen.

Deliverables:
- `features/transfer/SendPage.tsx` with steps `RecipientStep`, `AmountStep`, `ConfirmStep`,
  `ResultView`; hooks `useRecipientPreview`, `useSendTransfer`.
- Strings and error codes for the flow; unit, component and Playwright tests.

## Why
PRD-004 (synchronous P2P, masked recipient preview to avoid sending to the wrong person) and
PRD-005 UI-BR-27..30.

## How
### Modules and files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | Real `/send` route (T031 placeholder page and strings removed). Supports `/send?to=+2010…` prefill (from a future contact link; harmless now). |
| `web/src/features/transfer/SendPage.tsx` | Step state machine `recipient → amount → confirm → result`; a step indicator "Step n of 3" (`aria-live`). |
| `web/src/features/transfer/{RecipientStep,AmountStep,ConfirmStep,ResultView}.tsx` | Steps. |
| `web/src/features/transfer/hooks.ts` | `useRecipientPreview()` = mutation-style query on `GET /v1/recipients?phone=` (encoded by openapi-fetch; `+` → `%2B` — asserted in a test); `useSendTransfer()` used through `useIdempotentRequest('transfer', …)`. |
| `web/src/lib/validation.ts` | `validateNote` (≤ 140 chars after trim, counted as code points). |
| `web/src/i18n/en.ts` | Strings; `en.errors` adds `RECIPIENT_NOT_FOUND`, `SELF_TRANSFER_NOT_ALLOWED`, `RECIPIENT_LIMIT_EXCEEDED`, `INVALID_NOTE`. |
| `web/e2e/transfer.spec.ts` | E2E below. |

### Data (migrations)
None.

### API
None changed. Used: `GET /v1/recipients`, `POST /v1/transfers` (`Idempotency-Key`), `GET /v1/me/wallet`.

### Behaviour and design notes
- **Frozen wallet:** banner instead of the flow (UI-BR-14).
- **Step 1 (UI-BR-27):** phone field; invalid format → field error, no request. Continue →
  preview; success shows a card "Send to {masked_name}" + `formatPhone`, with "Continue" and
  "Change". Errors per UI-BR-27 under the field.
- **Step 2 (UI-BR-28):** `AmountField` + "Available: EGP …" + note textarea with "n/140" counter.
  Client checks: parseAmount ok, amount ≤ available balance (else "You don't have enough
  balance."), note valid. Back returns to step 1 keeping the phone.
- **Step 3 (UI-BR-29):** summary; Send → `POST /v1/transfers` body `{recipient_phone,
  amount_minor, currency: 'EGP', note?}` (note omitted when empty after trim). Idempotency per
  UI-BR-24: a key is reused only for exactly the same request after a retryable failure;
  Back and a changed value gives a new key.
- **Result (UI-BR-30):** 201 (or replay) → "Sent EGP X to {masked}", "New balance: EGP …"
  from `balance_after_minor`, links "View transaction" and "Send again" (fresh flow).
  `invalidateMoney` after success. `RECIPIENT_NOT_FOUND` → back to step 1 with the message;
  `INSUFFICIENT_FUNDS`, `DAILY_LIMIT_EXCEEDED`, `AMOUNT_OUT_OF_RANGE` (transfers wording),
  `RECIPIENT_LIMIT_EXCEEDED` → step 2 with the message (key reset); retryable errors stay on
  step 3 with "Try again" (same key); `WALLET_FROZEN` → refetch wallet, banner.
- Leaving the page mid-flow discards state (no persistence).

### Out of scope
Contacts / recent recipients, QR codes, requesting money, scheduled transfers.

## Acceptance Criteria
- [x] AC-1 — Given two registered users A (funded) and B, when A enters B's phone as `0100…`, then the preview shows B's masked name; A sends EGP 50 with note "lunch"; the result shows the new balance; both histories show the transfer (e2e).
- [x] AC-2 — Unknown phone → "No wallet uses this number."; own phone → "You can't send money to yourself."; bad format → field error with no request (component + e2e for unknown/own).
- [x] AC-3 — Amount above the available balance is rejected in the form; note of 141 chars rejected, 140 accepted (component, unit for `validateNote`).
- [x] AC-4 — The preview request encodes `+` as `%2B` (component asserting the URL).
- [x] AC-5 — Network error / 503 on Send → Try again resends with the same `Idempotency-Key`; Back + change amount uses a new key (component).
- [x] AC-6 — 422 `INSUFFICIENT_FUNDS` (race: balance dropped after step 2), `DAILY_LIMIT_EXCEEDED`, `RECIPIENT_LIMIT_EXCEEDED`, `AMOUNT_OUT_OF_RANGE` go back to step 2 with the right message; `RECIPIENT_NOT_FOUND` goes to step 1 (component).
- [x] AC-7 — Frozen sender sees the frozen banner on `/send` (e2e via internal freeze); a stale submit returning 403 `WALLET_FROZEN` shows the message (component).
- [x] AC-8 — Send button disabled while in flight; double-click sends one request (component). E2E: double click results in exactly one transfer in history.
- [x] AC-9 — Each step is keyboard usable; the step change is announced; fields have labels (component).
- [x] AC-10 — `make web-lint web-test web-build web-e2e` pass; CI green.

## Test Plan
| AC | Type | Test |
|----|------|------|
| 1 | e2e | `e2e/transfer.spec.ts` "happy path" |
| 2 | component + e2e | `SendPage.test.tsx` (step 1 cases); e2e "unknown / self" |
| 3 | unit + component | `validation.test.ts`, `SendPage.test.tsx` (step 2 cases) |
| 4, 5, 6, 8, 9 | component | `SendPage.test.tsx` |
| 7 | component + e2e | e2e "frozen sender" |
| 10 | CI | PR checks |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| Q1 | Must the user see the masked name before sending? | Yes, preview is a required step / optional | Answered | Required step (PRD-004 P2P-BR-5 intent) |
| Q2 | Check balance client-side? | Yes for fast feedback, API is authority / no | Answered | Yes |
| Q3 | Where do business errors return the user? | Step that can fix it (recipient → 1, amount → 2) / stay on confirm | Answered | Step that can fix it |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-12 | Q1–Q3 recommended options | Claude as PM under Yahya's standing mandate (2026-11-12) | PRD-005 §5.5 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-12 | Ready | Planned; implementation after T032 |
| 2026-11-12 | In Review | Implemented; review round 1 (review.md) |
| 2026-11-12 | Done | Review fixes applied; checks pass locally |

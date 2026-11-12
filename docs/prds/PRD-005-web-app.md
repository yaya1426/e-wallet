# PRD-005 — Web App (Wallet User UI)

| Field        | Value |
|--------------|-------|
| Status       | Accepted (2026-11-12, UI planning) |
| Owner        | Yahya Elaraby |
| Created      | 2026-11-12 |
| Depends on   | PRD-001, PRD-002, PRD-003, PRD-004 |
| Related TDRs | TDR-011, TDR-013, TDR-017, TDR-018, TDR-019, TDR-020, TDR-022 |

---

## 1. Summary
A responsive **web app** that lets a wallet user do everything the public API already allows:
sign up, log in, recover a password, see the balance and history, link a bank account, top up,
withdraw and send money to another user. A second area, the **operator console**, gives
operators the `/internal/v1` tools. The backend does not change for this PRD: the UI is a client
of `api/openapi/wallet.yaml`.

This lifts the PRD-001 §3 non-goal "Mobile / web front-end". Mobile apps stay out of scope.

## 2. Goals
| ID | Goal |
|----|------|
| UI-G1 | A user can run every wallet flow (PRD-002/003/004) from a browser on phone or desktop, without curl. |
| UI-G2 | The UI never shows a wrong amount: money is handled as integer piasters end to end (PRD-001 BR-2). |
| UI-G3 | Sessions behave like a normal app: stay signed in across reloads for up to 30 days, sign out works, no surprise logouts from token rotation (TDR-018 §3). |
| UI-G4 | Every API error code shows a clear message; nothing fails silently. |

## 3. Non-Goals
- Native mobile apps (iOS/Android), PWA install, offline mode, push notifications.
- Arabic / right-to-left layout in the first version (strings are kept in one file so it can be added; UI-BR-9).
- New backend features (profile editing, change password while signed in, SMS codes). The UI uses the API as it is.
- Dark mode, theming, marketing pages.

## 4. Delivery (one task per area, each planned on its own)
| # | Area | Screens |
|---|------|---------|
| 1 | Foundation + auth | App shell, sign-up, login, logout, password reset, session handling |
| 2 | Wallet home + history | Balance, transaction list, transaction detail |
| 3 | Bank account, top-up, withdrawal | Link/replace bank account, top-up, withdrawal and their status |
| 4 | P2P transfer | Recipient lookup (masked name), amount, confirmation, result |
| 5 | Operator console | Discrepancies, wallet freeze/unfreeze, reset codes, ops health |

## 5. Business Rules
### 5.1 Shared
- **UI-BR-1 Money.** Amounts are integer piasters from/to the API (`*_minor`). The UI never uses
  floating point for money. It shows amounts as `EGP 1,234.56` (two decimals, comma thousands
  separator). Amount input (tasks 3–4) is parsed from text into piasters with integer arithmetic.
- **UI-BR-2 Phone input.** The user may type an Egyptian mobile as `01XXXXXXXXX` (11 digits),
  `201XXXXXXXXX` or `+201XXXXXXXXX`; spaces and dashes are ignored. The UI converts it to E.164
  (`+201XXXXXXXXX`) before calling the API and checks it against the API pattern
  `^\+201[0125][0-9]{8}$`. Anything else is rejected in the form.
- **UI-BR-3 Client-side checks mirror the API** (to give fast feedback) but the API stays the
  authority: password 8–72 bytes (UTF-8), full name 2–100 characters after trimming, reset code
  exactly 6 digits. Server errors are always shown even if the form passed its own checks.
- **UI-BR-4 Error messages.** Each API error `code` maps to one user-facing message (the API's
  `message` is not shown). Unknown codes and network failures show a generic message plus the
  `request_id` when present, so support can find the log line.
- **UI-BR-5 Rate limits.** `429 TOO_MANY_REQUESTS` shows "Too many attempts. Please wait and try again."
  The UI does not retry by itself.
- **UI-BR-6 Disabled user.** `403 USER_DISABLED` at any point signs the user out and shows
  "Your account is disabled. Please contact support."

### 5.2 Auth screens (area 1)
- **UI-BR-7 Sign-up** asks for phone, full name, password and password confirmation (UI-only
  field; must match). On `201` the UI logs the user in with the same phone and password and
  opens the home screen. If that login fails, it opens the login screen with the phone filled in
  and a "Account created, please log in" notice.
- **UI-BR-8 Login** asks for phone and password. `401 INVALID_CREDENTIALS` shows "Wrong phone
  number or password." After login the user goes to the page they first asked for, or home.
- **UI-BR-9 Strings** live in one module (`en`), not inline in components.
- **UI-BR-10 Password reset** is operator-assisted (TDR-018 §4). The screen explains "Contact
  support to get a 6-digit reset code", then asks for phone, code, new password and
  confirmation. On `204` it opens the login screen with "Password changed. Please log in."
  `401 INVALID_RESET_CODE` shows "The code is wrong or expired. Ask support for a new one."
- **UI-BR-11 Sessions** stay signed in across page reloads and browser restarts until the
  refresh token expires (30 days) or the user signs out. Sign-out ends the session on the server
  (`POST /v1/auth/logout`) and in every open tab of the same browser.
- **UI-BR-12 Session end.** When the session can no longer be refreshed (`401
  INVALID_REFRESH_TOKEN`), the user is sent to login with "Your session has ended. Please log in again."

### 5.3 Wallet home and history (area 2)
- **UI-BR-13 Home** shows the available balance (`available_balance_minor`, UI-BR-1), the wallet
  status, quick actions (Top up, Withdraw, Send) and the 5 newest transactions with a "See all"
  link to the history screen.
- **UI-BR-14 Frozen wallet.** When the wallet status is `FROZEN`, home shows a banner: "Your
  wallet is frozen. You can still receive money, but you can't top up, withdraw or send. Please
  contact support." Top up, Withdraw and Send are disabled everywhere they appear. `403
  WALLET_FROZEN` from any money call shows the same text and refreshes the wallet.
- **UI-BR-15 Wallet ID.** Home has an "Account" section with the wallet ID and a copy button,
  labelled "Wallet ID (support may ask for it)". Operators freeze and unfreeze by wallet ID
  (UI-BR-33), and the API has no lookup by phone.
- **UI-BR-16 History** lists the user's transactions newest first, 20 per page, with a "Load
  more" button that uses `next_cursor` (hidden when it is `null`). Each row shows: a title
  (`Top-up`, `Withdrawal`, `Sent to <masked name>`, `Received from <masked name>`), the amount
  with a sign (`+` for `IN`, `−` for `OUT`), a status badge (`PENDING` and `PROCESSING` both show
  "Pending"; "Completed"; "Failed") and the date. Failed rows show the amount struck through,
  because no money moved. An empty history shows "No transactions yet."
- **UI-BR-17 Dates** are shown in Cairo time (`Africa/Cairo`, the same day boundary as the daily
  limits) as `12 Nov 2026, 14:05`.
- **UI-BR-18 Transaction detail** (`/transactions/:id`) shows type, status, direction, amount,
  counterparty (masked name and phone) for P2P, note, created and completed times, and for a
  failed transaction the reason: the bank `failure_code` mapped to a message (`INSUFFICIENT_FUNDS`
  "Your bank account didn't have enough money.", `INVALID_ACCOUNT` "The bank account details are
  not valid.", `ACCOUNT_CLOSED` "The bank account is closed."); unknown codes show "The bank
  rejected this request." with the code. A failed withdrawal adds "The money is back in your
  wallet." `404 TRANSACTION_NOT_FOUND` shows "Transaction not found." with a link to history.
- **UI-BR-19 Freshness.** The wallet and the first history page are refetched when the browser
  tab regains focus and after any money action. While a transaction on screen is `PENDING` or
  `PROCESSING`, the screen refetches every 5 seconds for at most 2 minutes.

### 5.4 Bank account, top-up and withdrawal (area 3)
- **UI-BR-20 Bank account screen** shows the linked account (IBAN in groups of four, holder name,
  last updated) or "No bank account linked yet." (`404 BANK_ACCOUNT_NOT_FOUND`). The form asks
  for IBAN and holder name. Client checks mirror the API: the IBAN, after removing spaces and
  upper-casing, matches `^EG[0-9]{27}$` and passes the ISO 13616 mod-97 check; the holder name is
  2–100 characters after trimming. Replacing an existing account asks "Replace your linked bank
  account? Future top-ups and withdrawals will use the new one." `INVALID_IBAN` and
  `INVALID_HOLDER_NAME` show next to their field.
- **UI-BR-21 Amount input** (top-up, withdrawal, P2P) is a text field with an `EGP` prefix. It
  accepts digits with an optional `.` and at most 2 decimals; commas and spaces are ignored
  (`1,250.5` → `125050` piasters). Zero, negatives, more than 2 decimals, any other character
  or a value above `Number.MAX_SAFE_INTEGER` piasters are rejected in the form (`parseAmount`,
  T030). The UI does **not** check the min/max limits itself, because they are server config
  (PRD-001 §5.6); `422 AMOUNT_OUT_OF_RANGE` shows "This amount is outside the allowed range for <top-ups |
  withdrawals | transfers>."
- **UI-BR-22 Linked account required.** Top-up and withdrawal first load the bank account; when
  none is linked the screen shows "Link a bank account first." with a link instead of the form.
  `422 BANK_ACCOUNT_REQUIRED` shows the same.
- **UI-BR-23 Confirmation.** Before sending, the UI shows "Top up EGP 500.00 from the account
  ending 5000?" / "Withdraw EGP 500.00 to the account ending 5000?" (last 4 IBAN digits) with
  Confirm and Back. A withdrawal also says "The amount leaves your available balance right away."
- **UI-BR-24 Idempotency keys** (top-up, withdrawal, P2P). A new UUID v4 `Idempotency-Key` is
  made when the user presses Confirm. If the call fails with a network error, `409
  REQUEST_IN_PROGRESS`, `503 TRY_AGAIN` or a 5xx, the "Try again" button sends the **same**
  request with the **same** key, so a double charge is impossible. The key is dropped when the
  user goes Back and changes anything, or after any other final answer. The Confirm button is
  disabled while a request is in flight.
- **UI-BR-25 Status after 202.** The UI opens a status screen for the new transaction and polls
  `GET /v1/transactions/{id}` every 2 seconds for up to 60 seconds: pending shows "Waiting for
  your bank…"; `COMPLETED` shows success and the refreshed balance; `FAILED` shows the reason
  (UI-BR-18). After 60 seconds without a final state: "This is taking longer than usual. It will
  finish on its own; you can follow it in your history." with a link to the transaction.
- **UI-BR-26 Error messages** for these flows: `WALLET_LIMIT_EXCEEDED` "This top-up would take
  your balance above the maximum allowed."; `INSUFFICIENT_FUNDS` "You don't have enough
  balance."; `DAILY_LIMIT_EXCEEDED` "This is above your daily limit for withdrawals and
  transfers."; `WALLET_FROZEN` per UI-BR-14; others per UI-BR-4.

### 5.5 P2P transfer (area 4)
- **UI-BR-27 Step 1, recipient.** The user enters a phone (UI-BR-2). "Continue" calls `GET
  /v1/recipients` and shows "Send to Ahmed M. A. (+20 100 123 4567)?" `RECIPIENT_NOT_FOUND`
  shows "No wallet uses this number."; `SELF_TRANSFER_NOT_ALLOWED` "You can't send money to
  yourself."; `INVALID_PHONE` "Enter a valid Egyptian mobile number."
- **UI-BR-28 Step 2, amount and note.** Amount per UI-BR-21, an optional note (at most 140
  characters after trimming, with a counter) and the available balance shown. An amount above
  the available balance is rejected in the form with "You don't have enough balance." (fast
  feedback; the API stays the authority).
- **UI-BR-29 Step 3, confirm.** A summary (recipient masked name and phone, amount, note) with
  Send and Back. Send calls `POST /v1/transfers` with keys per UI-BR-24.
- **UI-BR-30 Result.** On `201` the UI shows "Sent EGP X to <masked name>", the new balance
  (`balance_after_minor`) and a link to the transaction. `RECIPIENT_LIMIT_EXCEEDED` shows "The
  recipient can't receive this amount right now."; `RECIPIENT_NOT_FOUND` at this step goes back
  to step 1 with its message; others per UI-BR-26.

### 5.6 Operator console (area 5)
- **UI-BR-31 Access.** The console lives under `/ops` and does not use the user session. An
  unlock screen asks for the operator key and checks it with `GET /internal/v1/health/ops`: `401`
  shows "Wrong operator key."; `404` shows "The operator API is not enabled on this server." The
  key is kept as TDR-022 says. A "Lock" button forgets it, and any later `401` locks the console.
- **UI-BR-32 Ops health** is the console home: overall status (ok / warning / critical) with
  colour and text, open discrepancy counts, stuck transactions, outbox/inbox pending and dead
  counts, and a jobs table (name, last status, last finished, late). It refreshes every 30 seconds
  and with a Refresh button.
- **UI-BR-33 Wallet freeze/unfreeze.** A form takes a wallet ID (UUID, checked in the form) with
  Freeze and Unfreeze buttons; each asks for confirmation naming the wallet ID and then shows the
  status the API returns. `WALLET_NOT_FOUND` shows "No wallet with this ID." (There is no API to
  read a wallet's status, so the screen shows the result of the last action only.)
- **UI-BR-34 Discrepancies.** A list, newest first, filtered by status (default `OPEN`) and kind,
  50 per page with "Load more". Rows show severity, kind, created time and transaction ID. A
  detail view shows every field and `details` as indented JSON text. Resolve asks for a note (1–500
  characters after trimming) and a confirmation; `409 ALREADY_RESOLVED` reloads the item and
  says "Someone already resolved this discrepancy."
- **UI-BR-35 Reset codes.** A phone input (UI-BR-2) and confirmation "Issue a reset code for
  <phone>? Any earlier unused code stops working." On `201` the code is shown large with its
  expiry time (UI-BR-17) and a copy button, and it is cleared when the operator leaves the
  screen. `USER_NOT_FOUND` shows "No user with this phone number."; `USER_DISABLED` (once T029
  adds it) shows "This user is disabled; a reset code can't be issued."

## 6. Non-Functional Requirements
| ID | Requirement |
|----|-------------|
| UI-NFR-1 | Works on the last two versions of Chrome, Safari (incl. iOS), Firefox, Edge; layouts from 360 px wide. |
| UI-NFR-2 | Accessible forms: every input has a label, errors are linked to their field and announced (`aria-live`), full keyboard use. |
| UI-NFR-3 | Same origin as the API in every environment (TDR-019); no CORS. |
| UI-NFR-4 | Strict Content-Security-Policy; no third-party scripts, fonts or analytics (TDR-020). |
| UI-NFR-5 | Every screen has automated tests: component tests and end-to-end tests against the local compose stack. |

## 7. Open Questions
None (answered 2026-11-12: web app, React stack, same repo and origin, token storage, English only).

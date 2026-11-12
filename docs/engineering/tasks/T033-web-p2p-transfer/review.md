# Review — T033 (2026-11-12, rounds 1–2)

## Round 1 — Changes Requested (minor)
Independent read against task.md, PRD-005 §5.5 and the OpenAPI contract. Static state: tsc
clean, Vitest passing. No blocker or major findings.

| # | Severity | File | Finding | Fix | Status |
|---|----------|------|---------|-----|--------|
| 1 | Minor | `SendPage.tsx` | On `INSUFFICIENT_FUNDS` the user went back to step 2 with the old "Available" balance and the client check still passed. | Amount errors refetch the wallet. Test "INSUFFICIENT_FUNDS on send refetches the balance…". | Fixed |
| 2 | Minor | `useIdempotentRequest.ts` | A key kept in sessionStorage after an ambiguous failure could answer a deliberate identical transfer much later with the old replay. | Stored keys expire after 10 minutes (`STORED_KEY_TTL_MS`); test added. | Fixed |
| 3 | Minor | `ConfirmStep.tsx` | After a retryable error the focused Send button disappeared and focus fell to `<body>`. | Focus moves to "Try again" (or to the error for a final error). Test asserts it. | Fixed |
| 4 | Minor | `SendPage.tsx` | After the preview, focus went to the unchanged heading; the masked name was not read out. | Focus moves to the preview card. Keyboard test updated. | Fixed |
| 5 | Nit | `SendPage.test.tsx` | Double-click test only proved the button is disabled, not the in-flight guard. | Added "two Send clicks in the same tick". | Fixed |
| 6 | Nit | task.md | Test plan named per-step test files; "Back resets the key" wording. | task.md updated; deviation in implementation.md. | Fixed |
| 7 | Nit | `SendPage.tsx` | `?to=+20…` decodes `+` to a space. | Prefill is trimmed (normalisation already accepted it). | Fixed |

Checked and fine: request body from the server-echoed phone; note trimmed and omitted when
empty; error routing per UI-BR-27/30 and Q3; final answers drop the key; result screen shown
before any reload state; `validateNote` matches Go; keyboard path; e2e isolation.

## Round 2 — Approved
Fixes verified with tests. `make web-ci` and the browser tests pass (see implementation.md).
Verdict: **Approved**.

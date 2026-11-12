# T033 — Implementation

## Summary
Signed-in users can send money to another wallet from `/send` in three steps: recipient (with
the masked-name preview), amount and note, confirm. The result shows what was sent and the new
balance. No Go, API or migration changes.

## Files
| Path | Change |
|------|--------|
| `web/src/app/routes.tsx` | Real `/send` route; the T031 "coming soon" page and its strings are removed (no placeholder is left) |
| `web/src/features/transfer/SendPage.tsx` | Step state, error routing, focus management; `?to=` prefill |
| `web/src/features/transfer/{RecipientStep,AmountStep,ConfirmStep,ResultView}.tsx` | The steps |
| `web/src/features/transfer/hooks.ts` | `useRecipientPreview` (mutation on `GET /v1/recipients`), `useSendTransfer` |
| `web/src/lib/validation.ts` | `validateNote`, `noteLength` (code points after trimming, like Go) |
| `web/src/lib/useIdempotentRequest.ts` | A key left in `sessionStorage` by an earlier visit is reused for 10 minutes at most |
| `web/src/i18n/en.ts` | `en.transfer.*`; errors `RECIPIENT_NOT_FOUND`, `SELF_TRANSFER_NOT_ALLOWED`, `RECIPIENT_LIMIT_EXCEEDED`, `INVALID_NOTE` |
| `web/e2e/transfer.spec.ts` | Browser tests against the compose stack |

## Notes and deviations
- Component tests live in one `SendPage.test.tsx` (the steps only make sense together) instead of
  per-step files; the task's test plan is updated.
- Keys: the same `useIdempotentRequest` as T032 (scope `transfer`). The task said "Back from
  step 3 resets the key"; the code keeps a key only after a retryable failure and only for exactly
  the same request, so Back + Send of unchanged values cannot transfer twice, and any change gets a
  new key (UI-BR-24 wording). After review, a key stored by an earlier visit expires after 10
  minutes, so a deliberate identical transfer later is not answered with an old replay.
- Errors go to the step that can fix them (Q3): recipient errors → step 1 (field error, focus on
  the field); amount errors → step 2 (the wallet is refetched so "Available" is current); invalid
  note → step 2 note; `WALLET_FROZEN` → wallet refetch, frozen banner; retryable → stay on step 3
  with "Try again" focused.
- The request uses the phone the server echoed in the preview, not the raw input.

## AC → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | e2e "A sends EGP 50 with a note to B…" (desktop + mobile); `SendPage.test.tsx` "happy path" | ✅ |
| AC-2 | `SendPage.test.tsx` "bad phone format", preview error table; e2e "unknown number and own number" | ✅ |
| AC-3 | `validation.test.ts` "validateNote"; `SendPage.test.tsx` "amount above the available balance and a 141-character note" | ✅ |
| AC-4 | `SendPage.test.tsx` "encodes + as %2B" | ✅ |
| AC-5 | `SendPage.test.tsx` "network error / 503…"; `useIdempotentRequest.test.ts` | ✅ |
| AC-6 | `SendPage.test.tsx` 422 table, "INSUFFICIENT_FUNDS on send refetches…", "404 RECIPIENT_NOT_FOUND on send" | ✅ |
| AC-7 | `SendPage.test.tsx` "frozen wallet…", "a stale submit answered 403…"; e2e "a frozen sender" | ✅ |
| AC-8 | `SendPage.test.tsx` "Send is disabled while in flight…", "two Send clicks in the same tick"; e2e "double-clicking Send" | ✅ |
| AC-9 | `SendPage.test.tsx` "every step is keyboard usable", focus assertions | ✅ |
| AC-10 | `make web-ci`, `make web-e2e` locally; CI on the PR | ✅ locally |

# T009 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T009-recipient-preview`, stacked on T008 (PR #10, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- `identity.Service.PreviewRecipient(ctx, callerID, phone)`: malformed phone → `ErrInvalidPhone`, unknown → `ErrRecipientNotFound`, the caller's own → `ErrSelfRecipient`, otherwise `Recipient{Phone, MaskedName}` (via `MaskName`). It is a read-only lookup on the pool. Disabled users are shown like any other.
- `GET /v1/recipients?phone=…` behind `RequireAuth` → `200 {phone, masked_name}`, `400 INVALID_PHONE`, `404 RECIPIENT_NOT_FOUND`, `422 SELF_TRANSFER_NOT_ALLOWED`, `401`.
- `RECIPIENT_NOT_FOUND` and `SELF_TRANSFER_NOT_ALLOWED` moved from `transfer` to `identity`, since both endpoints use them and `identity` may not import `transfer`. `transfer` now uses `identity`'s constants.
- OpenAPI: the endpoint (with a note on escaping `+` as `%2B`) and the `Recipient` schema.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestPreviewRecipient` "registered" (200, masked "Ahmed M. A.") | ✅ |
| AC-2 | `TestPreviewRecipient`: unknown → 404, own → 422, `01001234568` → 400, unescaped `+` → 400, missing → 400, no token → 401 | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 367 passed |
| `make up` + `make e2e` | ✅ |
| Live curl | registered → 200 "Ahmed M. A."; own → 422; unknown → 404; `01001234567` → 400 |

## Deviations from the plan
- The two shared codes moved to `identity` (see Summary). The values are unchanged.

## Follow-ups (not done)
- None. Rate limiting the lookup is out of scope (task).

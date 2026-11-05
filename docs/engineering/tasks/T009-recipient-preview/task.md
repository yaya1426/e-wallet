# T009 — Recipient preview

| Field        | Value |
|--------------|-------|
| ID           | T009 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T009-recipient-preview` |
| Depends on   | T004, T008 (`MaskName`) |
| PRDs         | PRD-004 P2P-BR-5, Q7 |
| TDRs         | TDR-013 |
| Docs changed | — |

## What
`GET /v1/recipients?phone=+201…` lets the sender confirm who they are about to pay. It returns the
masked name only.

## Why
PRD-004 Q7 (answered yes, 2026-11-05) and P2P-BR-5. TDR-013 endpoint list.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/identity/http.go` | Handler behind `RequireAuth` |
| `internal/identity/service.go` | `PreviewRecipient(ctx, callerID, phone) (Recipient, error)` |
| `api/openapi/wallet.yaml` | Endpoint |

### Data (migrations)
None.

### API
`GET /v1/recipients?phone=%2B201001234567` → `200 { "phone": "+201001234567", "masked_name": "Ahmed M. A." }`

| Error | HTTP | Code |
|-------|------|------|
| No/bad token | 401 | `UNAUTHENTICATED` |
| `phone` missing or invalid format | 400 | `INVALID_PHONE` |
| No user with that phone | 404 | `RECIPIENT_NOT_FOUND` |
| Phone is the caller's own | 422 | `SELF_TRANSFER_NOT_ALLOWED` |

### Behaviour and design notes
- A read-only lookup (no tx). It uses `identity.MaskName` from T008.
- A `DISABLED` recipient is shown like any other user (P2P to them is still allowed; PRD has no rule against it).
- The login rate limiter is **not** applied. Abuse protection of this endpoint is out of scope in phase 1.

### Out of scope
Rate limiting the lookup, showing the wallet status.

## Acceptance Criteria
- [ ] AC-1 — Given a registered phone, then `200` with the phone and masked name.
- [ ] AC-2 — Unknown → `404 RECIPIENT_NOT_FOUND`; own phone → `422 SELF_TRANSFER_NOT_ALLOWED`; missing/invalid (e.g. `01001234567`, unescaped `+` turned into a space) → `400 INVALID_PHONE`; no token → `401`.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2 | integration HTTP | `TestPreviewRecipient` (table) |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Masking, unknown phone, self lookup | defaults / stronger masking | Answered | Defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | 200 `{phone, masked_name}`; 404 `RECIPIENT_NOT_FOUND`; 422 `SELF_TRANSFER_NOT_ALLOWED`; 400 `INVALID_PHONE` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

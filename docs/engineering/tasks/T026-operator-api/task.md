# T026 — Operator API: discrepancies and wallet freeze

| Field        | Value |
|--------------|-------|
| ID           | T026 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T026-operator-api` |
| Depends on   | T016 (discrepancy), T021–T023 (what gets recorded) |
| PRDs         | PRD-001 US-9, BR-8 (frozen wallet) |
| TDRs         | TDR-018 §5, TDR-009 §3, TDR-016, TDR-013 |
| Docs changed | — |

## What
`/internal/v1/*` endpoints for operators, protected by `INTERNAL_API_KEY`: list and resolve
discrepancies, freeze and unfreeze a wallet. The key is set on DigitalOcean (generated, encrypted).

## Why
TDR-018 §5, TDR-009 §3 ("operators read discrepancies via GET /internal/v1/discrepancies"); PRD-001
US-9 (decided 2026-11-10).

## How
### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/platform/httpserver/internalkey.go` | `RequireInternalKey(key)` middleware (header `X-Internal-Api-Key`, constant-time) |
| `internal/discrepancy/service.go`, `queries.sql`, `http.go` | `Resolve(ctx, id, note)`, cursor paging in `List`, handler |
| `internal/wallet/service.go`, `queries.sql`, `http.go` | `SetStatus(ctx, walletID, status)`; internal routes |
| `internal/platform/config` | `INTERNAL_API_KEY` (optional; when set ≥ 32 bytes) |
| `cmd/wallet/app.go` | Mount internal routes only when the key is set; log "internal API enabled/disabled" |
| `api/openapi/wallet.yaml` | `internal` tag, endpoints, `apiKey` security scheme, codes |

### API (all need `X-Internal-Api-Key`; wrong/missing → `401 UNAUTHENTICATED`)
- `GET /internal/v1/discrepancies?status=OPEN|RESOLVED&kind=…&limit=1..100(default 50)&cursor=…`
  → `200 {items:[{id, kind, severity, transaction_id|null, details, status, created_at, resolved_at|null,
  resolution_note|null}], next_cursor|null}`, newest first. Bad status/kind/limit/cursor → `400`
  `INVALID_FILTER` / `INVALID_LIMIT` / `INVALID_CURSOR`.
- `POST /internal/v1/discrepancies/{id}/resolve` `{note: 1..500 chars}` → `200` the discrepancy;
  `404 DISCREPANCY_NOT_FOUND`; already resolved → `409 ALREADY_RESOLVED`; bad note → `400 INVALID_NOTE`.
- `POST /internal/v1/wallets/{wallet_id}/freeze` and `/unfreeze` → `200 {wallet_id, status}` (idempotent);
  `404 WALLET_NOT_FOUND`; bad id → `404`.
- Every operator action is logged at INFO (`action`, ids, note length).

### Behaviour
- Resolve: `UPDATE … SET status='RESOLVED', resolved_at=now, resolution_note=note WHERE id AND status='OPEN'`.
  Never changes money (TDR-016). A new problem of the same kind can then be recorded again.
- Freeze/unfreeze only change `wallets.status` (PRD-001 BR-8 effects already implemented).
- Cursor = base64url of `(created_at, id)` like the history API (TDR-013).

### Out of scope
Operator identities/roles, ledger adjustments (ADJUSTMENT entries), password reset codes (T027),
ops health (T028).

## Acceptance Criteria
- [x] AC-1 — No/wrong key → 401 on every internal route; key not configured → internal routes 404.
- [x] AC-2 — List: filters by status and kind, newest first, paging with `next_cursor`, validation errors.
- [x] AC-3 — Resolve: OPEN → RESOLVED with note and time; second resolve → 409; unknown → 404; note validation.
- [x] AC-4 — Freeze → user's transfers/withdrawals/top-ups get `WALLET_FROZEN`, incoming still credited; unfreeze → allowed again; both idempotent; unknown wallet → 404.
- [x] AC-5 — Config validation for `INTERNAL_API_KEY`; OpenAPI documents everything.
- [x] AC-6 — On DO: `INTERNAL_API_KEY` set as an encrypted secret; `GET /internal/v1/discrepancies` with the key → 200, without → 401 (recorded).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-4 | integration HTTP (cmd/wallet) | `TestInternal_Auth`, `TestInternal_Discrepancies`, `TestInternal_Resolve`, `TestInternal_FreezeUnfreeze` |
| AC-5 | unit | `TestConfig_InternalAPIKey`, OpenAPI catalogue |
| AC-6 | manual on DO | curl |

## Open Questions
| # | Question | Options | Status | Answer |
|---|----------|---------|--------|--------|
| 1 | Operator API scope | discrepancies + reset codes + freeze / list only | Answered | Discrepancies (list, resolve) + freeze/unfreeze here; reset codes in T027 |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Operator API scope and `X-Internal-Api-Key` (TDR-018 §5) | Yahya | TDR-018 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | Approved | Implemented; review round 1: 1 Nit |
| 2026-11-10 | Done | Finalized — PR opened |

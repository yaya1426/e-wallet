# T010 — Transaction history API

| Field        | Value |
|--------------|-------|
| ID           | T010 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T010-transaction-history-api` |
| Depends on   | T006, T008 |
| PRDs         | PRD-001 US-4, US-5; PRD-004 P2P-BR-2, P2P-2 |
| TDRs         | TDR-013 (pagination, conventions), architecture §3.2 |
| Docs changed | — |

## What
A user lists their transactions (newest first, cursor pagination) and opens one. P2P shows up for
both sides with their own direction.

Deliverables:
- `GET /v1/me/transactions`, `GET /v1/transactions/{id}`.
- `transaction.ListForWallet`, `transaction.GetVisible`.
- Small resolver interfaces so `transaction` gets wallet IDs and party names without importing
  `wallet`/`identity`.
- OpenAPI additions.

## Why
PRD-001 US-4/US-5; PRD-004 P2P-BR-2 (one record, two directions) and P2P-2 (the sender's
name/phone shown to the recipient); TDR-013 cursor pagination, newest first.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/transaction/http.go` | 2 handlers (behind `RequireAuth`) |
| `internal/transaction/service.go`, `queries.sql` | Keyset query, visible get |
| `internal/transaction/cursor.go` | Encode/decode the opaque cursor |
| `internal/transaction/ports.go` | `WalletOfUser` and `Parties` interfaces (below) |
| `cmd/wallet/app.go` | Small adapters: `WalletOfUser` ← `wallet.GetByUserID`; `Parties` ← wallet + `identity.GetUser` + `identity.MaskName` |
| `api/openapi/wallet.yaml` | 2 endpoints |

```text
type WalletOfUser interface { WalletID(ctx, userID uuid.UUID) (uuid.UUID, error) }
type Party struct { Phone, MaskedName string }
type Parties interface { ByWallet(ctx, walletIDs []uuid.UUID) (map[uuid.UUID]Party, error) } // one batch per page
```

### Data (migrations)
None. The indexes from T006 (`source_wallet_id, created_at DESC, id DESC` and the destination one)
serve the keyset query.

### API
**`GET /v1/me/transactions?limit=20&cursor=<opaque>`** → `200`
```json
{ "items": [ {
    "id": "…", "type": "P2P", "status": "COMPLETED", "direction": "OUT",
    "amount_minor": 5000, "currency": "EGP",
    "counterparty": { "phone": "+201…", "masked_name": "Ahmed M. A." },
    "note": "lunch", "failure_code": null, "failure_message": null,
    "created_at": "…", "updated_at": "…", "completed_at": "…" } ],
  "next_cursor": "…" }
```
- `direction`: `OUT` when the caller's wallet is the source (withdrawal, P2P sent), `IN` when it is
  the destination (top-up, P2P received).
- `counterparty`: only for P2P (the other side), otherwise `null`.
- `next_cursor`: `null` on the last page.
- `limit`: default 20, 1–100.

**`GET /v1/transactions/{id}`** → `200` with one item (same shape).

| Error | HTTP | Code |
|-------|------|------|
| No/bad token | 401 | `UNAUTHENTICATED` |
| `limit` not an integer in 1–100 | 400 | `INVALID_LIMIT` |
| Cursor not decodable | 400 | `INVALID_CURSOR` |
| `{id}` not a UUID, not found, or not visible to the caller | 404 | `TRANSACTION_NOT_FOUND` |

### Behaviour and design notes
- **Visible** = the caller's wallet is `source_wallet_id` or `destination_wallet_id`.
- **Keyset pagination**: order by `(created_at DESC, id DESC)`. Cursor = base64url (no padding) of
  JSON `{"t":"<RFC3339Nano created_at>","id":"<uuid>"}`; next page = rows strictly "before"
  `(t, id)`. Implementation: `UNION ALL` of the source-wallet and destination-wallet index scans
  (each `ORDER BY … LIMIT n+1`), merged and cut to `limit` (a P2P to oneself cannot exist, so there
  are no duplicates). Fetch `limit+1` to know if there is a next page.
- All reads, no tx. All times UTC RFC 3339.

### Out of scope
Filters (type/status/date), statements/exports, showing the bank reference to the user.

## Acceptance Criteria
- [x] AC-1 — Given a user with a top-up (IN), a withdrawal (OUT), a P2P sent (OUT) and a P2P received (IN) (inserted through the services), then the list shows all 4 newest first with correct direction; counterparty is only on the P2P rows, showing the other user.
- [x] AC-2 — Given 45 transactions and `limit=20`, then pages of 20, 20, 5; `next_cursor` is null on the last page; no item is repeated or skipped, even with equal `created_at` values (fake clock).
- [x] AC-3 — `limit=0`, `101`, `abc` → `400 INVALID_LIMIT`; a garbage cursor → `400 INVALID_CURSOR`.
- [x] AC-4 — `GET /v1/transactions/{id}` for own (either side of a P2P) → `200`; for another user's transaction, an unknown ID, or a non-UUID → `404 TRANSACTION_NOT_FOUND`.
- [x] AC-5 — A failed top-up shows `status: FAILED` with `failure_code` and `failure_message`.
- [x] AC-6 — The party lookup is one batch call per page (the test counts calls on a fake `Parties`).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-5 | integration HTTP | `TestListMyTransactions_DirectionsAndCounterparty` |
| AC-2 | integration | `TestListMyTransactions_Pagination` |
| AC-3 | unit | `TestParseLimit`, `TestDecodeCursor` |
| AC-4 | integration HTTP | `TestGetTransaction_Visibility` |
| AC-6 | unit | `TestList_BatchesParties` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Cursor, filters, 404 vs 403, item fields | defaults / add filters | Answered | Defaults, no filters |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Opaque base64url cursor of (created_at, id); limit 20 (1–100); no filters; others' → 404; party names through interfaces wired in main | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | implement-task started, stacked on T009 (local) and T008 (PR #10) |
| 2026-11-09 | In Review | Implemented and verified: make ci ok, 374 tests |
| 2026-11-09 | Approved | Review round 1: Approved (1 Minor recorded as follow-up) |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

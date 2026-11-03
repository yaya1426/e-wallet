# TDR-013 — Identifiers, Time and API Conventions

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-03, during T001 planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | TDR-002, TDR-005, TDR-012 |

## Decision

### Identifiers
- All primary keys are **UUIDv7** generated in Go (`internal/platform/id`). Time-ordered →
  good index locality; non-guessable enough for external exposure.
- The transaction ID doubles as the bank idempotency key (TDR-005 layer 3).

### Time
- All timestamps `timestamptz` in UTC; API returns RFC 3339 UTC (`2026-11-03T12:00:00Z`).
- A `clock.Clock` interface is injected everywhere time matters (tests control time).
- Business days (daily limits, daily settlement) use `Africa/Cairo`.

### API conventions
- Base path `/v1`; internal operator endpoints `/internal/v1`; bank webhooks `/webhooks/bank`.
- JSON, `snake_case` fields. Amounts: `amount_minor` (int64) + `currency`.
- Strict decoding: unknown fields rejected (`DisallowUnknownFields`), body max 64 KB.
- Error body (all errors):
  ```json
  { "error": { "code": "INSUFFICIENT_FUNDS", "message": "Available balance is too low.", "request_id": "…" } }
  ```
- Status codes: `201` created & completed (P2P), `202` accepted for async (top-up, withdrawal),
  `400` malformed, `401` unauthenticated, `403` forbidden/frozen, `404` not found,
  `409` in progress, `422` business rule, `503` try again.
- Pagination: cursor based (`?limit=20&cursor=<opaque>`), newest first.
- Every response has `X-Request-ID` (generated if not supplied) — also logged.

### Endpoints (phase 1)
| Method | Path | PRD |
|--------|------|-----|
| POST | `/v1/auth/register` | PRD-001 |
| POST | `/v1/auth/login` | PRD-001 |
| PUT | `/v1/me/bank-account` | PRD-001 US-6 |
| GET | `/v1/me/wallet` | PRD-001 US-3 |
| GET | `/v1/me/transactions` | PRD-001 US-4 |
| GET | `/v1/transactions/{id}` | PRD-001 US-5 |
| POST | `/v1/topups` | PRD-002 |
| POST | `/v1/withdrawals` | PRD-003 |
| POST | `/v1/transfers` | PRD-004 |
| GET | `/v1/recipients?phone=` | PRD-004 Q7 |
| POST | `/webhooks/bank` | TDR-007 |
| GET | `/internal/v1/discrepancies` | TDR-009 |
| GET | `/healthz`, `/readyz`, `/metrics` | ops |

The full contract will be written in `api/openapi/wallet.yaml` before implementation.

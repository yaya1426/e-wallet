# T015 — Bank webhook ingestion (inbox)

| Field        | Value |
|--------------|-------|
| ID           | T015 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T015-webhook-inbox` |
| Depends on   | T011 |
| PRDs         | PRD-002 §4, PRD-003 §4, PRD-001 NFR-4, NFR-8, NFR-9 |
| TDRs         | TDR-007, TDR-005 L4, TDR-006 (queue mechanics) |
| Docs changed | — |

## What
The bank calls `POST /webhooks/bank`; we verify the signature, store the event in `inbox_events`
(deduplicated), answer `200` fast, and inbox workers hand each event to a registered handler in
its own DB transaction. The handler (payment) is wired in T016.

## Why
TDR-007 §1 (endpoint, HMAC + 5-minute window, `ON CONFLICT DO NOTHING`, fast 200, 5xx only when
storing failed), §2 (table), §3 (workers on the shared queue, unknown transaction → retry → `DEAD`),
§4 (event types/payload); TDR-005 L4; NFR-9 (raw payload kept). Decided 2026-11-05: `webhook`
defines an interface that `payment` implements; queue defaults.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000008_inbox.up/down.sql` | Table |
| `internal/webhook/http.go` | `POST /webhooks/bank` (no user auth) |
| `internal/webhook/signature.go` | `Verify(secret, header, body, now) error` |
| `internal/webhook/model.go` | `BankEvent` (all TDR-007 §4 fields, amount as `money.Money`), `EventHandler`, `ErrUnknownTransaction` |
| `internal/webhook/service.go`, `store.go`, `queries.sql` | Insert, `queue.Store`, runner |
| `internal/platform/config` | `BANK_WEBHOOK_SECRET` (required, ≥ 32 bytes), `INBOX_ENABLED=true`, `INBOX_WORKERS=4`, `INBOX_MAX_ATTEMPTS=10` |
| `cmd/wallet/app.go` | Mount the route; start the inbox runner when enabled **and** a handler is set (T016 sets it) |
| `.golangci.yml` | depguard: `webhook` imports only platform (+ `discrepancy` from T016) |

```text
type EventHandler interface {
    // Runs inside the worker's DB transaction. Return ErrUnknownTransaction to retry later.
    HandleBankEvent(ctx context.Context, tx pgx.Tx, ev BankEvent) error
}
func (s *Service) SetHandler(h EventHandler)
```

### Data (migrations)
```sql
inbox_events(id uuid PK, provider text NOT NULL, provider_event_id text NOT NULL,
  event_type text NOT NULL, payload jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('PENDING','PROCESSING','DONE','FAILED','DEAD')),
  attempts int NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL,
  locked_by text NULL, locked_until timestamptz NULL, last_error text NULL,
  received_at timestamptz NOT NULL, processed_at timestamptz NULL,
  UNIQUE (provider, provider_event_id))
INDEX (status, next_attempt_at) WHERE status IN ('PENDING','PROCESSING')
```
`provider` = `'banksim'` in phase 1.

### API
`POST /webhooks/bank` — header `X-Bank-Signature: t=<unix>,v1=<hex>`.

| Case | HTTP | Code |
|------|------|------|
| Stored, or a duplicate `event_id` | 200 | `{"status":"ok"}` |
| Header missing/malformed, bad HMAC, or `abs(now − t) > 5 min` | 401 | `INVALID_SIGNATURE` |
| Body > 64 KB | 413 | `REQUEST_TOO_LARGE` |
| Body is not JSON, or `event_id` / `event_type` missing | 400 | `MALFORMED_REQUEST` |
| Insert failed (DB down) | 503 | `TRY_AGAIN` (the bank retries) |

The signature is checked on the **raw** body before parsing. Comparison is constant-time
(`hmac.Equal`). Several `v1=` values in the header: any match is accepted.

### Behaviour and design notes
- The handler only verifies, parses the envelope, and inserts (`ON CONFLICT DO NOTHING`), then
  calls the runner's `Wake()`. No business logic (TDR-007 §1).
- **Worker** per event: decode the full payload into `BankEvent`. A payload that cannot be decoded,
  or an unknown `event_type` (not one of `debit.succeeded|debit.failed|payout.succeeded|payout.failed`)
  → `Fail(reason)` → status `FAILED` (no retry). Otherwise `InTx(handler.HandleBankEvent)`:
  - nil → `Done`;
  - `ErrUnknownTransaction` → `Retry` (it may race with the outbox commit) → after
    `INBOX_MAX_ATTEMPTS` → `DEAD` (T016 adds the `UNKNOWN_BANK_EVENT` discrepancy on dead);
  - other errors → `Retry`.
- Marking the inbox row happens after the business tx commits (separate statement). A crash in
  between re-delivers the event, and the handler is idempotent (state machine + ledger
  `UNIQUE (transaction_id, entry_type)`, TDR-005 L4).
- Logs: `event_id`, `event_type`, `bank_reference`, result. Never log the secret or the signature.
- Metrics: `webhook_received_total{result}` (`stored`, `duplicate`, `invalid_signature`, `malformed`, `error`) + the queue metrics with `queue="inbox"`.

### Out of scope
Applying outcomes (T016/T019), the `UNKNOWN_BANK_EVENT` discrepancy (T016), multiple providers.

## Acceptance Criteria
- [ ] AC-1 — A correctly signed event → `200`, one `PENDING` row with the raw payload; the fake handler receives it in a tx, and the row becomes `DONE`.
- [ ] AC-2 — The same event posted 5 times → 5 × `200`, one row, the handler called once (PRD-002 AC-4).
- [ ] AC-3 — Wrong secret, tampered body, missing header, `t` 6 minutes old or 6 minutes in the future → `401 INVALID_SIGNATURE`, nothing stored.
- [ ] AC-4 — Malformed JSON / missing `event_id` → `400`; > 64 KB → `413`; DB unavailable → `503 TRY_AGAIN`.
- [ ] AC-5 — Handler returns `ErrUnknownTransaction` → retried with backoff, then `DEAD` after max attempts. Unknown `event_type` → `FAILED` without calling the handler.
- [ ] AC-6 — The handler's DB writes and its error roll back together (a handler that writes then errors leaves nothing behind, and the event is retried).
- [ ] AC-7 — A signature produced by banksim's `Sign` (T014) verifies with `Verify` (cross-check with a fixed vector).
- [ ] AC-8 — Config validation for the 4 variables; with `INBOX_ENABLED=false` the endpoint still stores but nothing is processed.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2 | integration HTTP | `TestWebhook_StoreAndProcess`, `TestWebhook_DuplicateDelivery` |
| AC-3 | unit | `TestVerify` (table) |
| AC-4 | unit + integration | `TestWebhook_BadRequests`, `TestWebhook_DBDown_503` |
| AC-5, AC-6 | integration | `TestInbox_UnknownTransactionThenDead`, `TestInbox_UnknownTypeFailed`, `TestInbox_HandlerRollback` |
| AC-7 | unit | `TestVerify_KnownVector` (same vector as T014) |
| AC-8 | unit | `TestConfig_Inbox` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Handler interface shape; queue defaults | interface in webhook / generic SQL | Answered | Interface in webhook, defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | `EventHandler` interface implemented by payment (wired in main); `INBOX_*` defaults; unknown txn → retry → DEAD; codes `INVALID_SIGNATURE`, `MALFORMED_REQUEST`, `TRY_AGAIN` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

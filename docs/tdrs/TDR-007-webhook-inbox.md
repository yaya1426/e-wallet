# TDR-007 — Bank Webhook Ingestion via Inbox Queue

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-002, PRD-003, TDR-005, TDR-006, TDR-009 |

## Context
The bank answers asynchronously by calling our webhook. If we process the webhook inline and
fail mid-way (DB blip, bug, deploy), the bank may or may not retry. We must never drop a bank
notification, must handle duplicates, and must answer the bank quickly.

## Decision

### 1. Endpoint
`POST /webhooks/bank` (separate route group, no user auth).

1. **Verify signature**: header `X-Bank-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + raw_body)>`.
   Reject if invalid or `|now − t| > 5 min` (replay protection) → `401`.
2. **Parse minimal envelope**: `event_id`, `event_type`, `occurred_at`.
3. **Store** in `inbox_events` with `INSERT … ON CONFLICT (provider, provider_event_id) DO NOTHING`.
4. Return **`200 OK`** immediately (also for duplicates). Return `5xx` only if the insert itself
   failed, so the bank retries.

No business logic runs in the HTTP handler.

### 2. Inbox table
`inbox_events`: `id UUID, provider ('banksim'), provider_event_id, event_type, payload JSONB
(raw body kept for audit), status (PENDING | PROCESSING | DONE | FAILED | DEAD), attempts,
next_attempt_at, locked_until, last_error, received_at, processed_at`,
`UNIQUE (provider, provider_event_id)`.

### 3. Inbox workers
Same dispatcher/worker-pool mechanics as the outbox (TDR-006), sharing the generic queue code
in `internal/platform/queue`, with its own pool (default 4 workers).

Processing one event (one DB transaction):
1. Find the transaction by `bank_reference`, or by `idempotency_key` (= our transaction ID)
   if the reference is not yet stored (webhook arrived before the outbox worker saved it).
2. `SELECT … FOR UPDATE` the transaction row.
3. Apply the shared **`payment.ApplyBankOutcome(ctx, tx, txnID, outcome)`** function — the
   **same function** reconciliation uses (TDR-009):
   - already terminal with same outcome → no-op;
   - terminal with different outcome → record a discrepancy through the `discrepancy` module
     (`discrepancy.Record(ctx, tx, …)`, same DB tx; TDR-016), no money change;
   - amount mismatch → discrepancy, no money change;
   - otherwise post ledger entry (TDR-004) and move status.
4. Mark inbox event `DONE`.

Unknown transaction (no match) → retried with backoff (it may be a race with the outbox
commit) up to max attempts, then `DEAD` + discrepancy `UNKNOWN_BANK_EVENT`.

### 4. Event types (bank → wallet)
`debit.succeeded`, `debit.failed`, `payout.succeeded`, `payout.failed`.
Payload: `event_id, event_type, bank_reference, idempotency_key, amount_minor, currency,
status, failure_code, failure_message, occurred_at`.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Process inline in the handler | A failure loses or delays the event depending on bank retry policy. |
| Queue in memory (channel) | Lost on crash. |

## Consequences
- + No dropped webhooks; duplicates harmless; bank gets fast `200`.
- + One code path for "bank told us the outcome" (webhook or reconciliation).
- − Small delay between receipt and balance update (sub-second normally).

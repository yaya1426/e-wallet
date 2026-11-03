# TDR-006 — Transactional Outbox + Goroutine Worker Pool (no message broker)

| Field      | Value |
|------------|-------|
| Status     | Proposed |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-002, PRD-003, TDR-002, TDR-005, TDR-007, TDR-008 |

## Context
A top-up or withdrawal must (a) be stored in our DB and (b) be sent to the bank. Doing (b)
inside the HTTP request is fragile: the bank may be slow/down, and a crash between commit and
the bank call loses the request ("dual write" problem). We need a durable queue, but we do
not want to run a broker (Kafka, RabbitMQ, Pub/Sub) in this phase.

## Decision

### 1. Outbox table (the queue lives in PostgreSQL)
`outbox_events`:
`id UUID PK, aggregate_type, aggregate_id (transaction_id), event_type
('topup.requested' | 'withdrawal.requested'), payload JSONB, status
(PENDING | PROCESSING | DONE | DEAD), attempts INT, next_attempt_at timestamptz,
locked_by TEXT, locked_until timestamptz, last_error TEXT, created_at, processed_at`.
Index: `(status, next_attempt_at)` partial `WHERE status IN ('PENDING','PROCESSING')`.

The event row is inserted **in the same DB transaction** as the business transaction (and,
for withdrawals, the reserve ledger entry). Commit = durable intent.

### 2. Dispatcher + workers (goroutines)
```
                ┌───────────────┐   jobs chan (buffered, size = 2×N)   ┌───────────┐
DB outbox ◀─────│  Dispatcher   │ ───────────────────────────────────▶ │ Worker 1…N│──▶ Bank
  (poll)        │ (1 goroutine) │                                      └───────────┘
                └───────────────┘
```
- **Dispatcher** (1 goroutine per instance) every `poll_interval` (default 500 ms, plus an
  immediate wake-up signal from the API after commit via a non-blocking channel send) claims a
  batch:
  ```sql
  UPDATE outbox_events SET status='PROCESSING', locked_by=$instance,
         locked_until=now()+interval '60 seconds', attempts=attempts+1
  WHERE id IN (SELECT id FROM outbox_events
               WHERE (status='PENDING' AND next_attempt_at<=now())
                  OR (status='PROCESSING' AND locked_until<now())   -- crashed worker
               ORDER BY next_attempt_at LIMIT $batch
               FOR UPDATE SKIP LOCKED)
  RETURNING *;
  ```
  and sends the rows to a buffered channel.
- **Workers**: a fixed pool of `N` goroutines (default 8) reading from the channel; each
  calls the handler registered for the `event_type` (handler registry in `internal/outbox`,
  handlers live in `internal/payment`).
- `SKIP LOCKED` + lease (`locked_until`) makes it safe to run many instances and recovers
  events from crashed workers automatically.
- Each handler call has its own `context.WithTimeout` (default 10 s) for the bank call.

### 3. Handler outcome → event state
| Outcome | Event | Transaction |
|---------|-------|-------------|
| Bank accepted (202 + `bank_reference`) | `DONE` | `PENDING → PROCESSING`, store `bank_reference` (same DB tx) |
| Bank business rejection (4xx with reason) | `DONE` | `→ FAILED` (+ refund entry for withdrawals) |
| Technical error (timeout, 5xx, connection) | back to `PENDING`, `next_attempt_at = now() + backoff(attempts)` | unchanged |
| `attempts >= max_attempts` (default 10) | `DEAD` | unchanged — handed to reconciliation (TDR-009) |

Backoff: exponential with full jitter, base 1 s, cap 5 min
(`sleep = rand(0, min(cap, base·2^attempts))`). 10 attempts ≈ up to ~20 minutes total.

### 4. Delivery guarantee
**At-least-once** delivery to the bank + **bank idempotency key = transaction_id** (TDR-005)
= effectively-once execution at the bank.

### 5. Lifecycle
- Started from `main` under an `errgroup` with a root context; on SIGTERM the dispatcher
  stops claiming, workers finish current jobs (bounded by shutdown timeout, default 25 s),
  unfinished leases simply expire and are reclaimed after restart.
- A janitor goroutine archives `DONE` events older than 7 days (moves to
  `outbox_events_archive`), keeping the hot table small.

### 6. Metrics
`outbox_pending_total`, `outbox_oldest_pending_age_seconds`, `outbox_attempts_total{result}`,
`outbox_dead_total`, `bank_call_duration_seconds{op,result}`.

## Alternatives Considered
| Option | Why not chosen now |
|--------|--------------------|
| Call bank inside HTTP request | Lost requests on crash, user waits on bank latency. |
| Message broker (Kafka / RabbitMQ / Google Pub/Sub / NATS) | Extra infra; still needs an outbox to be atomic with DB. **Future option**: keep the outbox, add a relay that publishes to the broker. |
| In-memory channel only (no DB table) | Lost on crash. |
| Postgres `LISTEN/NOTIFY` as the queue | Not durable; used at most as an optional wake-up signal. |
| Library (e.g. River, a Postgres job queue for Go) | Good option; rejected for now to keep the flow explicit and learnable. Revisit if our hand-rolled queue grows features. |

## Consequences
- + No lost bank work, crash-safe, horizontally scalable via `SKIP LOCKED`.
- + Zero new infrastructure.
- − Polling adds DB load (small at our scale; tuned with batch size and interval).
- − Ordering is not guaranteed across events (not needed: each transaction has one event).

## Revisit When
Outbox throughput > a few hundred events/sec, other consumers need our events, or we split
services — then add a broker relay behind the same outbox.

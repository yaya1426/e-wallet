# T012 — Outbox module

| Field        | Value |
|--------------|-------|
| ID           | T012 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T012-outbox-module` |
| Depends on   | T011 |
| PRDs         | PRD-001 NFR-3, NFR-4, NFR-10 |
| TDRs         | TDR-006, TDR-001 §4 (role flags) |
| Docs changed | `engineering/roadmap.md` (archive janitor → phase 2) |

## What
The transactional outbox: business code enqueues an event in its own DB transaction; the outbox
runner delivers it to the registered handler, at least once.

Deliverables:
- `outbox_events` table (the archive table comes with the janitor in phase 2).
- `internal/outbox`: `Enqueue(ctx, tx, Event)`, `Register(eventType, Handler)`, `Run(ctx)`, `Wake()`, sqlc `Store` for `platform/queue`.
- Config + wiring in `cmd/wallet` behind `OUTBOX_ENABLED`.
- Metrics `outbox_pending_total`, `outbox_oldest_pending_age_seconds`, `outbox_dead_total`.

## Why
TDR-006 §1 (table), §2 (dispatcher/workers, wake-up after commit), §3 (outcomes), §5 (lifecycle),
§6 (metrics); NFR-3 (the bank request is stored in the same tx as the business transaction).
Decided 2026-11-05: the archive janitor (TDR-006 §5) is moved to phase 2.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000007_outbox.up/down.sql` | Table + partial index |
| `internal/outbox/model.go` | `Event{AggregateType, AggregateID, Type, Payload any}`, `Handler`, errors |
| `internal/outbox/service.go` | `Enqueue`, `Register`, `Run`, `Wake` |
| `internal/outbox/store.go`, `queries.sql` | `queue.Store` implementation |
| `internal/platform/config` | `OUTBOX_ENABLED=true`, `OUTBOX_WORKERS=8`, `OUTBOX_MAX_ATTEMPTS=10`, `QUEUE_POLL_INTERVAL=500ms` (shared with the inbox), constants: lease 60 s, handler timeout 10 s |
| `cmd/wallet/app.go` | Start `outbox.Run` in the errgroup when enabled |
| `.golangci.yml` | depguard: `outbox` imports only platform |

### Data (migrations)
```sql
outbox_events(id uuid PK, aggregate_type text NOT NULL, aggregate_id uuid NOT NULL,
  event_type text NOT NULL, payload jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('PENDING','PROCESSING','DONE','DEAD')),
  attempts int NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL,
  locked_by text NULL, locked_until timestamptz NULL, last_error text NULL,
  created_at timestamptz NOT NULL, processed_at timestamptz NULL)
INDEX (status, next_attempt_at) WHERE status IN ('PENDING','PROCESSING')
INDEX (aggregate_id)
```

### API
None.

### Behaviour and design notes
- `Enqueue(ctx, tx, ev)`: insert `PENDING`, `next_attempt_at = now`, payload = `json.Marshal(ev.Payload)`.
  An unregistered event type → `ErrUnknownEventType` (checked at enqueue so mistakes fail fast).
- `Register` must be called before `Run`. Registering a type twice panics (wiring bug).
- Claim SQL as in TDR-006 §2. `MarkDone` sets `DONE` + `processed_at`. `MarkFailed` (a `Fail` result)
  also ends as `DONE` with `last_error = reason`, because for the outbox a business rejection is
  a finished delivery (TDR-006 §3 "Bank business rejection → event DONE").
- **Wake-up after commit**: `Wake()` is a non-blocking signal. Callers call it **after** their
  `InTx` returned nil (never inside the tx). T017/T018 do this.
- A handler gets `queue.Job`. It decodes the payload itself and returns `Done | Retry | Fail`.
- Gauges `outbox_pending_total` and `outbox_oldest_pending_age_seconds` are refreshed by the
  dispatcher every 15 s with one `SELECT count(*), min(created_at) … WHERE status IN ('PENDING','PROCESSING')`.
  `outbox_dead_total` counts `MarkDead`.
- Role flag: with `OUTBOX_ENABLED=false`, `Enqueue` still works (API-only replica) but nothing is delivered.

### Out of scope
The archive janitor and `outbox_events_archive` (phase 2), the real handlers (T017, T018).

## Acceptance Criteria
- [x] AC-1 — An event enqueued in a tx that rolls back is never delivered; one enqueued in a committed tx is delivered once to its handler with the same payload.
- [x] AC-2 — Two runners (two "instances") on the same DB with 100 events → each event handled exactly once by a `Done` handler (`SKIP LOCKED`).
- [x] AC-3 — A handler that crashes the "instance" (test cancels the runner mid-handler without marking) → after the lease expires (short lease in the test), another runner claims and handles it; attempts = 2.
- [x] AC-4 — `Retry` → back to `PENDING` with `next_attempt_at` in the future and `last_error` set; after `OUTBOX_MAX_ATTEMPTS` → `DEAD`, `outbox_dead_total` +1.
- [x] AC-5 — `Fail("INVALID_ACCOUNT")` → status `DONE`, `last_error = "INVALID_ACCOUNT"`.
- [x] AC-6 — `Wake()` after commit → delivered well before `QUEUE_POLL_INTERVAL` would have (test with a 10 s poll).
- [x] AC-7 — Enqueue of an unregistered type → `ErrUnknownEventType`.
- [x] AC-8 — `OUTBOX_ENABLED=false` → the runner does not start; config defaults/validation as listed.
- [x] AC-9 — Gauges reflect pending count and oldest age.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | integration | `TestOutbox_CommitDelivers_RollbackDoesNot` |
| AC-2 | integration | `TestOutbox_TwoRunnersExactlyOnce` |
| AC-3 | integration | `TestOutbox_ExpiredLeaseReclaimed` |
| AC-4, AC-5 | integration | `TestOutbox_RetryThenDead`, `TestOutbox_FailEndsDone` |
| AC-6 | integration | `TestOutbox_WakeAfterCommit` |
| AC-7 | unit | `TestEnqueue_UnknownType` |
| AC-8 | unit | `TestConfig_Outbox`, `TestServe_OutboxDisabled` |
| AC-9 | integration | `TestOutbox_Gauges` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Archive janitor phase | phase 2 / now | Answered | Phase 2 |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Janitor/archive → phase 2; `OUTBOX_ENABLED/WORKERS/MAX_ATTEMPTS`, `QUEUE_POLL_INTERVAL` defaults; lease 60 s; `Fail` → `DONE` | Yahya | roadmap, this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | Implementation started |
| 2026-11-09 | In Review | Implemented and verified; see implementation.md |
| 2026-11-09 | Approved | Review round 1: Approved (1 Minor accepted, 2 Nits) |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

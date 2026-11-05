# TDR-009 — Reconciliation Job and Scheduler

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-002 §7, PRD-003 §7, TDR-004 §6, TDR-006, TDR-007, TDR-008 |

## Context
Webhooks can be lost (network, bank bug, our endpoint down for longer than the bank retries).
Outbox events can go `DEAD` with unknown bank state. The ledger's view of the company bank
account must match the bank's. We need a background job that detects and resolves this.

## Decision

### 1. Scheduler: in-process goroutine + `time.Ticker` + Postgres advisory lock
- `internal/reconciliation` runs a **scheduler goroutine** started by `main` (role flag
  `RECON_ENABLED`, default `true`).
- Jobs are plain Go functions registered with an interval. Each tick:
  1. `SELECT pg_try_advisory_lock(<job_key>)` on a dedicated connection.
     Not acquired → another instance is running it → skip this tick.
  2. Run the job with a timeout context; release the lock; record a row in `job_runs`
     (`job_name, started_at, finished_at, status, items_checked, items_fixed, error`).
- Interval jobs use `time.Ticker` with jitter; the daily job computes the next run from the
  `Africa/Cairo` wall clock.

**Why this choice:** no new infrastructure, works with one or many instances (the advisory
lock guarantees a single runner), lives with the code it checks, and fits our goroutine
approach. Library `robfig/cron` was considered only for cron expressions; two fixed jobs
don't need it.

### 2. Jobs
| Job | Interval | What it does |
|-----|----------|--------------|
| `pending_bank_ops` | every 1 min | Transactions `PROCESSING` older than **10 min** (Q5) and not checked in the last 5 min, batch 100: call `bank.GetOperation(transaction_id)` and apply the result through **`payment.ApplyBankOutcome`** (same code as webhooks). Still pending → update `last_reconciled_at`, try again later. |
| `dead_outbox` | every 5 min | Outbox events `DEAD`: query the bank by key. Not found → transaction `FAILED` (`BANK_UNAVAILABLE`) + refund for withdrawals. Found → store `bank_reference`, set `PROCESSING`, continue. |
| `stuck_alert` | every 15 min | Any non-terminal bank transaction older than **24 h** → discrepancy `STUCK_TRANSACTION`, metric + alert. Money stays as is (reserved for withdrawals). |
| `ledger_integrity` | every 1 h | Checks TDR-004 §6 items 1–3. |
| `daily_settlement` | 02:00 Africa/Cairo | Pull `GetStatement(yesterday)`; match each bank movement to a transaction by `bank_reference`; compare totals with ledger postings on `BANK_SETTLEMENT` for that day; compare closing balance. Unmatched on either side → discrepancy. |

### 3. Discrepancies
Owned by the small **`discrepancy` module** (TDR-016, decided 2026-11-05), not by `reconciliation`, so that
`payment` (via `ApplyBankOutcome`), `webhook` and `reconciliation` can all record one without
breaking the import rules (architecture §3.2). It exposes `Record(ctx, tx, …)` and `List`.
Table `reconciliation_discrepancies`: `id, kind (STATUS_MISMATCH | AMOUNT_MISMATCH |
UNMATCHED_BANK_MOVEMENT | UNMATCHED_LEDGER_MOVEMENT | STUCK_TRANSACTION | UNKNOWN_BANK_EVENT |
LEDGER_INTEGRITY), severity, transaction_id NULL, details JSONB, status (OPEN | RESOLVED),
created_at, resolved_at, resolution_note`.

**Auto-fix policy:** the job automatically resolves only cases where the bank gives a clear
final answer for a non-terminal transaction (this is the "missed webhook" case). Anything that
contradicts a terminal state, or any amount/balance mismatch, is **never auto-fixed** — it is
recorded for an operator. Operator fixes are new ledger entries (`ADJUSTMENT` via `SUSPENSE`),
never edits.

Operators read discrepancies via `GET /internal/v1/discrepancies` (internal role) in this phase.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| `robfig/cron` in process | Fine, but only adds cron syntax we don't need; still needs a distributed lock. |
| OS cron / Kubernetes CronJob running `wallet reconcile` | Good for production later; needs extra deploy config; harder in local dev. The job code is written so a `wallet reconcile --job=X` sub-command can call the same functions. |
| `pg_cron` | Logic would move into SQL; bank API calls impossible from SQL. |
| Separate scheduler service | Contradicts TDR-001. |

## Consequences
- + Lost webhooks and unknown outcomes self-heal within ~11–15 min.
- + Single-runner safety without extra infrastructure.
- − Scheduler shares process resources with the API (jobs are light and batched).

## Revisit When
Multiple deployments with strict scheduling SLAs, or many more jobs → external scheduler
(K8s CronJob) invoking the same sub-command.

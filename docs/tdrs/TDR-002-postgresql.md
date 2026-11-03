# TDR-002 — Database: PostgreSQL

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-03, during T001 planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | TDR-004, TDR-006, TDR-010, `engineering/data-model.md` |

## Context
We need a reliable, ACID database with row-level locking, strong constraints and a
work-queue capability (for the outbox) without adding another infrastructure component.

## Decision
- **PostgreSQL 18** (image `postgres:18-alpine`), pinned in `deployments/docker-compose.yml`, CI and testcontainers. (Decided in T001.)
- One database `wallet`, one schema `public`, plain table names. Which module owns which table
  is recorded in `engineering/data-model.md` and enforced in code review (TDR-001 module rules).
  Schema-per-module was rejected as extra ceremony for one service.
- Default isolation level **READ COMMITTED** + explicit row locks (`SELECT … FOR UPDATE`) on
  wallet accounts for money movement (TDR-010).
- Constraints do the last line of defence:
  - `CHECK (amount > 0)` on postings and transactions,
  - `CHECK (balance >= 0)` on user wallet accounts,
  - `UNIQUE` constraints for idempotency keys, bank event IDs and bank references,
  - foreign keys everywhere,
  - a deferred constraint trigger that rejects any journal entry whose debits ≠ credits.
- Work queues (outbox, inbox) use `SELECT … FOR UPDATE SKIP LOCKED`.
- Single-runner jobs use **advisory locks** (`pg_try_advisory_lock`) (TDR-009).
- Timestamps: `timestamptz`, stored in UTC; business-day calculations use `Africa/Cairo`.
- Money columns: `BIGINT` (TDR-003). No `NUMERIC`, no `MONEY` type, no floats.
- Primary keys: `UUID` (v7, generated in Go — TDR-013).
- Migrations: **golang-migrate** (superseded goose — see TDR-014), SQL files in `migrations/`, applied by a `wallet migrate` sub-command
  and in CI. Forward-only in production; `down` scripts only for local dev.
- Driver/access: **pgx v5** + **sqlc** (TDR-012).

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| MySQL | Weaker CHECK/deferred constraint support historically; fewer queueing primitives. |
| CockroachDB / distributed SQL | Not needed at this scale; serializable retries add complexity. |
| Separate Redis for idempotency / queues | Extra moving part; loses atomicity with business writes. |

## Consequences
- + Atomic business + ledger + outbox writes; the DB itself guards key invariants.
- − Postgres is a single point of failure → backups + PITR required before production.

## Revisit When
Write throughput on the ledger exceeds what one primary can handle, or read load requires replicas.

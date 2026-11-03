# TDR-001 — Architecture Style: Modular Monolith in Go

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-03, during T001 planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-001, TDR-012, `engineering/architecture.md` |

## Context
The wallet needs strong consistency between business transactions, the ledger and the outbox.
These must be written **in one ACID database transaction**. Splitting them across services
would force distributed transactions or sagas for every money movement, which adds failure
modes we do not need at this stage. At the same time we want clear module boundaries so the
code stays maintainable and can be split later if ever needed.

## Decision
1. We build **one deployable Go service: `wallet`** (binary `cmd/wallet`), backed by **one
   PostgreSQL database**.
2. Inside the service, code is split into **business modules** under `internal/` (identity,
   wallet, ledger, transaction, payment, bank, outbox, webhook, reconciliation, idempotency) plus
   shared **platform** packages (config, database, http server, logging, metrics).
3. **Module rules**
   - Each module owns its tables. No module reads or writes another module's tables
     directly; it calls the owning module's exported Go API (service interface).
   - Cross-module calls are **in-process function calls**, not HTTP.
   - Atomic work across modules uses a shared **unit of work**: a `pgx.Tx` is started by the
     orchestrating use case and passed to each module's methods (`…(ctx, tx, …)`). This is
     how "transaction + ledger + outbox" commit together.
   - Dependency direction is one-way (see architecture.md §4). Cycles are forbidden; enforced
     in CI with a dependency lint (`go-arch-lint` or a `depguard` rule set).
4. The service runs several roles **in the same process**, each started as goroutines by
   `cmd/wallet/main.go`: HTTP API, outbox dispatcher + workers, webhook inbox workers,
   reconciliation scheduler. Each role can be switched on/off by config flags so that later
   we can run, e.g., API-only and worker-only replicas of the **same binary**.
5. The **bank simulator** is *not* part of the wallet. It represents an external system and
   lives in the same repository as a separate binary (`cmd/banksim`) used only for
   development and tests (TDR-008).

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Microservices (wallet, ledger, payments…) | Distributed transactions / sagas for every movement, network failure between our own parts, more ops. Not justified now. |
| Classic layered monolith (handlers/services/repos at top level) | Boundaries erode; every package depends on every other. |
| Hexagonal per module with separate Go modules (`go.work`) | Extra tooling overhead; one Go module with `internal/` packages gives enough isolation. |

## Consequences
- + Money consistency is a single DB commit. Simple to test and operate.
- + Boundaries are explicit; a module can be extracted later behind the same interface.
- − One process: a bad deploy affects all roles (mitigated by role flags and graceful shutdown).
- − Discipline needed to keep modules from reaching into each other's tables.

## Revisit When
Independent scaling needs per module, separate teams per module, or a second product
needing the ledger as a shared platform.

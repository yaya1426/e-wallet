# TDR-012 — Go Project Layout and Technology Stack

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-03, during T001 planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | TDR-001, TDR-002, `engineering/architecture.md` §5 |

## Context
We want a layout any Go engineer recognizes, without inventing our own conventions, and a
small, boring, well-maintained set of libraries.

## Decision

### 1. Layout — community standard (`golang-standards/project-layout`) + `internal/` modules
```
e-wallet/
├── cmd/
│   ├── wallet/            # main: wires config, DB, modules; starts API, workers, scheduler
│   └── banksim/           # main: external bank simulator (dev/test only)
├── internal/              # private application code (not importable from outside)
│   ├── platform/          # shared technical building blocks, no business logic
│   │   ├── config/  database/  httpserver/  logger/  metrics/
│   │   ├── money/   clock/     id/          queue/   (generic DB-backed queue for outbox/inbox)
│   ├── identity/          # users, bank accounts, auth
│   ├── wallet/            # wallet API: get wallet, balance, limits
│   ├── ledger/            # accounts, journal entries, postings — the only writer of money
│   ├── transaction/       # transactions table, state machine, history queries
│   ├── payment/           # top-up & withdrawal use cases, ApplyBankOutcome
│   ├── transfer/          # P2P use case
│   ├── idempotency/       # API idempotency middleware + store
│   ├── outbox/            # outbox dispatcher, handler registry
│   ├── webhook/           # bank webhook endpoint + inbox processing
│   ├── reconciliation/    # scheduler + recon jobs
│   ├── discrepancy/       # reconciliation_discrepancies (added by TDR-016)
│   ├── bank/              # Gateway port + banksim HTTP adapter
│   └── banksim/           # simulator implementation used by cmd/banksim
├── api/
│   ├── openapi/wallet.yaml   # public API contract (source of truth)
│   └── openapi/banksim.yaml  # simulator contract
├── migrations/            # golang-migrate SQL migrations (TDR-014)
├── configs/               # example .env files
├── Dockerfile             # at the root (TDR-015; was deployments/)
├── docker-compose.yml     # at the root (TDR-015; was deployments/)
├── scripts/               # helper scripts (make targets call these)
├── test/                  # e2e tests & fixtures (black-box against running containers)
├── docs/                  # PRDs, TDRs, engineering docs
├── Makefile
├── go.mod
└── README.md
```
`pkg/` is **not** created: nothing is meant to be imported by other projects.

### 2. Inside each business module
```
internal/<module>/
├── service.go        # exported API used by other modules + use cases
├── model.go          # domain types, errors, state machines
├── repository.go     # interface + pgx/sqlc implementation
├── queries.sql       # sqlc input for this module's tables
├── http.go           # HTTP handlers for this module (if any)
└── *_test.go
```
Handlers → service → repository. Handlers never touch the DB. Other modules use only
`service.go` exports.

### 3. Stack
| Concern | Choice |
|---------|--------|
| Go version | **Go 1.27**: `go 1.27` + `toolchain go1.27.3` in `go.mod`; CI uses `go-version-file: go.mod` (decided in T001; first chosen 1.25, changed to 1.27 during T001 implementation because 1.25 is end-of-life and current dependencies need ≥ 1.26) |
| Module path | `github.com/yaya1426/e-wallet` (decided in T001) |
| CLI | stdlib `flag` with a sub-command switch (`serve`, `migrate`, later `reconcile`). No CLI framework. |
| HTTP router | **stdlib `net/http`** `ServeMux` with method + path patterns (Go ≥ 1.22). No framework. |
| DB driver / pool | **`jackc/pgx/v5`** + `pgxpool` |
| Queries | **`sqlc`** generates type-safe Go from SQL (no ORM) |
| Migrations | ~~`pressly/goose`~~ → **`golang-migrate/migrate/v4`** (superseded by TDR-014) |
| Logging | stdlib **`log/slog`** JSON handler |
| Config | env vars, parsed by **`caarlos0/env`**; `.env` only for local dev |
| Metrics | **`prometheus/client_golang`**, `/metrics` endpoint |
| Tracing | OpenTelemetry — deferred (not phase 1) |
| Validation | hand-written validation in request structs (few endpoints) |
| IDs | **`google/uuid`** v7 (TDR-013) |
| Concurrency helpers | `golang.org/x/sync/errgroup` |
| Testing | stdlib `testing`, **`stretchr/testify`**, **`testcontainers-go`** (real Postgres in tests) |
| Lint | **`golangci-lint`** incl. `forbidigo` (no floats in money code), `depguard` (module boundaries) |
| API docs | OpenAPI 3.1 file written by hand (spec-first) |
| Local env | **Docker Compose**: `postgres`, `wallet`, `banksim` |
| Container images | Multi-stage: `golang:1.27` builder (`CGO_ENABLED=0`) → **`gcr.io/distroless/static-debian12:nonroot`** runtime. Go binaries also import `time/tzdata` so `Africa/Cairo` always resolves. (decided in T001) |
| Repository / CI | GitHub `yaya1426/e-wallet`. GitHub Actions on every `pull_request` to `main` and every `push` to `main`: tidy check, gofmt, go vet, golangci-lint, build, unit tests (`-race`), integration tests (testcontainers, PostgreSQL 18), docker compose smoke test (`/healthz`, `/readyz`). `main` is protected: PR + green checks required, no force-push, no required approvals. (decided in T001) |

### 4. Process wiring (`cmd/wallet/main.go`)
`config.Load → logger → pgxpool → modules (constructor injection, no DI framework) →
errgroup{ httpServer.Run, outbox.Run, inbox.Run, reconciliation.Run, idempotency.Cleanup }`
with `signal.NotifyContext(SIGINT, SIGTERM)` for graceful shutdown. Sub-commands:
`wallet serve` (default), `wallet migrate up|status`, `wallet reconcile --job=<name>`.

## Consequences
- + Familiar layout and small dependency set; sqlc keeps SQL explicit (important for locking queries).
- − No ORM conveniences; we write SQL (deliberate).

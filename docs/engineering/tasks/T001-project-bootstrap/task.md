# T001 — Project bootstrap: Go skeleton, local env and CI

| Field        | Value |
|--------------|-------|
| ID           | T001 |
| Status       | Done |
| Created      | 2026-11-03 |
| Branch       | `task/T001-project-bootstrap` |
| Depends on   | — |
| PRDs         | PRD-001 §8 (NFR-7 observability, NFR-10 graceful shutdown) |
| TDRs         | TDR-001, TDR-002, TDR-012, TDR-013, TDR-014 |
| Docs changed | TDR-001, TDR-002, TDR-012, TDR-013 → `Accepted`; TDR-002 → PostgreSQL 18; TDR-012 → Go 1.27 (changed from 1.25 during implementation), module path, CLI, container images, repository/CI; new TDR-014 (golang-migrate, supersedes goose in TDR-002/012); `engineering/architecture.md` → PostgreSQL 18; `docs/README.md` index statuses |

## What
A runnable, empty-of-business-logic repository that every later task builds on, with CI that
gates merges.

Deliverables:
- Git repository + private GitHub repo `yaya1426/e-wallet`; the existing `docs/` and
  `.claude/skills/` go in as the first commit on `main`.
- Go module `github.com/yaya1426/e-wallet` with the TDR-012 layout.
- `cmd/wallet` with sub-commands `serve` (default) and `migrate up|status`.
- `cmd/banksim` stub that only serves `/healthz` (real simulator is a later task).
- `internal/platform`: `config`, `logger`, `database`, `httpserver`, `metrics`.
- `migrations/` with golang-migrate (TDR-014), embedded in the binary, and one baseline migration.
- Endpoints `/healthz`, `/readyz`, `/metrics`.
- `Makefile`, `.golangci.yml`, Dockerfile, `deployments/docker-compose.yml`,
  `configs/.env.example`, `.gitignore`, root `README.md`.
- GitHub Actions CI + branch protection on `main`.

## Why
- TDR-001/TDR-012 fix the architecture and layout; every feature task needs this skeleton.
- `implement-task` verification (`.claude/skills/implement-task/references/verification.md`)
  needs the `make` targets, a runnable service and integration tests with Postgres.
- `finalize-task` refuses to merge without CI and a GitHub remote.
- TDR-013 defines `/healthz`, `/readyz`, `/metrics`, `X-Request-ID` and the error body;
  PRD-001 NFR-7 (logs/metrics) and NFR-10 (graceful shutdown).

## How

### Modules and files
| Path | Change |
|------|--------|
| `.gitignore` | Go binaries, `bin/`, `coverage*`, `.env`, OS/IDE files |
| `README.md` | Project intro, link to `docs/README.md`, prerequisites, `make` targets, how to run locally |
| `go.mod` / `go.sum` | `module github.com/yaya1426/e-wallet`, `go 1.27`, `toolchain go1.27.3` |
| `cmd/wallet/main.go` | Sub-command switch with stdlib `flag`: `serve` (default when no argument), `migrate up`, `migrate status`. Unknown sub-command → usage on stderr, exit code 2. Wiring: `config.Load → logger.New → database.Open → httpserver` under `signal.NotifyContext(SIGINT, SIGTERM)` + `errgroup` |
| `cmd/banksim/main.go` | Loads its own config, serves `GET /healthz` only, graceful shutdown; nothing else |
| `internal/platform/config` | Wallet `Config` struct parsed with `caarlos0/env`: `DATABASE_URL` (required), `HTTP_ADDR` (default `:8080`), `LOG_LEVEL` (`debug\|info\|warn\|error`, default `info`), `SHUTDOWN_TIMEOUT` (default `25s`). Banksim `Config`: `BANKSIM_HTTP_ADDR` (default `:9090`), `LOG_LEVEL`. Missing or invalid values → error naming the variable |
| `internal/platform/logger` | `slog` JSON handler on stdout, level from config |
| `internal/platform/database` | `Open(ctx, url) (*pgxpool.Pool, error)` with a ping; `Ping(ctx)`. (`InTx` / unit of work is **not** in T001) |
| `internal/platform/httpserver` | `Server` wrapping `http.Server` (read-header/read/write/idle timeouts), `Run(ctx)` that shuts down gracefully within `SHUTDOWN_TIMEOUT` when ctx ends; middleware: request ID (keep the incoming `X-Request-ID` or generate a UUIDv7, set it on the response and in the logger context), access log (method, path, status, duration, request_id), panic recovery → 500 with the error body; `WriteError(w, status, code, message)` producing the TDR-013 error body; `/healthz`, `/readyz` handlers based on a `ReadinessCheck` list |
| `internal/platform/metrics` | Prometheus registry with Go/process collectors and `http_requests_total{method,route,status}` and `http_request_duration_seconds{method,route}`; `/metrics` handler |
| `migrations/embed.go` | `package migrations`; `//go:embed *.sql` → `FS` |
| `migrations/000001_baseline.up.sql` / `000001_baseline.down.sql` | Both contain only `SELECT 1;` and a comment that this marks the schema start (no schema change) |
| `internal/platform/database/migrate.go` | golang-migrate (`golang-migrate/migrate/v4`, source `iofs` over `migrations.FS`, database driver `pgx/v5`): `MigrateUp`, `MigrationStatus` (current version, dirty flag, latest embedded version), and `MigrationState(ctx)` returning `ok` / `pending` (DB version < latest embedded, or no version yet) / `fail` (dirty or DB unreadable). `ErrNoChange` from `Up` is success |
| `Makefile` | `tools` (installs golangci-lint and the `migrate` CLI with the `postgres` tag), `fmt`, `fmt-check`, `tidy-check`, `vet`, `lint`, `build`, `test`, `test-integration`, `cover`, `up`, `down`, `migrate` (runs `wallet migrate up`), `migrate-down` (`migrate` CLI, local only), `migration name=<x>` (`migrate create -ext sql -dir migrations -seq -digits 6`), `e2e` (compose smoke), `ci` (all checks in CI order) |
| `.golangci.yml` | golangci-lint **v2** config. Linters: govet, staticcheck, errcheck, ineffassign, unused, revive, gosec, bodyclose, errorlint, misspell, nolintlint, forbidigo, depguard. Formatters: gofmt, goimports. `forbidigo`: ban `float32`/`float64` in `internal/{ledger,wallet,payment,transfer}/**` and `internal/platform/money/**` (TDR-003). `depguard`: `internal/platform/**` must not import business modules (`internal/{identity,wallet,ledger,transaction,payment,transfer,idempotency,outbox,webhook,reconciliation,bank,banksim}`). The exact golangci-lint version (latest v2.x on implementation day) is pinned the same in the Makefile and CI and recorded in `implementation.md` |
| `deployments/Dockerfile` | Multi-stage with `ARG APP` (`wallet` or `banksim`): `golang:1.27` builder, `CGO_ENABLED=0`, `-trimpath`, then `gcr.io/distroless/static-debian12:nonroot`; `ENTRYPOINT ["/app"]` |
| `deployments/docker-compose.yml` | `postgres` (`postgres:18-alpine`, db/user/password `wallet`, `pg_isready` healthcheck, volume); `migrate` (wallet image, `command: ["migrate","up"]`, runs once after postgres is healthy); `wallet` (`:8080`, starts after `migrate` completed successfully); `banksim` (`:9090`) |
| `configs/.env.example` | All variables above with local-dev values; no real secrets |
| `test/e2e/smoke_test.go` | `//go:build e2e`; reads `WALLET_URL` / `BANKSIM_URL`; checks health endpoints of the running compose stack |
| `.github/workflows/ci.yml` | See CI below |
| Imports `time/tzdata` | In both `main` packages, so `Africa/Cairo` resolves in distroless (TDR-012) |

Empty business-module directories are **not** created; each module's task creates its own package.

### Data (migrations)
One baseline migration `000001_baseline` (up + down) with no schema change; applying it creates
golang-migrate's `schema_migrations` table and gives `/readyz` a version to compare against.
No business tables. Migrations are applied only by `wallet migrate up` (compose `migrate`
service, CI, tests) — `wallet serve` never migrates (TDR-002, TDR-014).

### API
| Method | Path | Response |
|--------|------|----------|
| GET | `/healthz` | `200 {"status":"ok"}` — process is up; no dependency checks |
| GET | `/readyz` | Ready: `200 {"status":"ok","checks":{"database":"ok","migrations":"ok"}}`. Not ready: `503 {"status":"unavailable","checks":{"database":"fail","migrations":"pending"}}`. `database` = `ok`/`fail` (ping with a 2 s timeout); `migrations` = `ok`/`pending`/`fail` (`fail` when the version is dirty or the DB can't be read) |
| GET | `/metrics` | Prometheus text format |
| any other | — | `404` with the TDR-013 error body, code `NOT_FOUND` |
| wrong method on a known path | — | `405` with the TDR-013 error body, code `METHOD_NOT_ALLOWED` |
| panic in a handler | — | `500` with the error body, code `INTERNAL_ERROR` (no stack trace in the body; stack logged) |

Banksim: `GET /healthz` → `200 {"status":"ok"}`.
Every response carries `X-Request-ID`. Health and metrics responses are excluded from the
access log at `info` (logged at `debug`) so they don't flood the logs.

### CI (`.github/workflows/ci.yml`)
- Triggers: `pull_request` to `main`, `push` to `main`. `concurrency` cancels superseded runs for the same ref.
- Go installed with `actions/setup-go` using `go-version-file: go.mod`, module cache on.
- Jobs (job names are the required status checks):
  1. **`lint`** — `make tidy-check fmt-check vet lint` (golangci-lint via its official action, pinned version).
  2. **`test`** — `make build test` (`go test -race -count=1 ./...` + coverage summary).
  3. **`integration`** — `make test-integration` (testcontainers `postgres:18-alpine`; Docker is on `ubuntu-latest`).
  4. **`smoke`** — `docker compose -f deployments/docker-compose.yml up -d --build --wait`, then `make e2e`; always `docker compose logs` on failure and `down -v` at the end.

### Repository setup (part of this task)
1. `git init -b main` in the project root; `.gitignore`; **first commit directly on `main`** with
   `docs/`, `.claude/skills/` and `.gitignore` (message `docs: initial PRDs, TDRs, architecture and task workflow skills`).
2. `gh repo create yaya1426/e-wallet --private --source . --remote origin --push`.
3. `git switch -c task/T001-project-bootstrap` and do all code work there (merged later by `/finalize-task T001`).
4. Branch protection for `main` (after the first CI run exists so the check names are known —
   done by finalize-task once the PR's checks have run, before merging): require a PR, require
   status checks `lint`, `test`, `integration`, `smoke` to pass and branch up to date, block
   force-push and deletion, **no required approvals**, admins not exempted.
   **If GitHub refuses because the private repo is on a free plan → make the repo public
   (`gh repo edit --visibility public --accept-visibility-change-consequences`) and apply the
   protection** (user decision, 2026-11-03).

### Behaviour and design notes
- `serve`: start the HTTP server; on SIGINT/SIGTERM stop accepting connections, let in-flight
  requests finish up to `SHUTDOWN_TIMEOUT`, close the DB pool, exit 0. A failure to start (bad
  config, DB unreachable at startup) → log the error and exit 1.
- DB unreachable **after** startup does not crash the process; `/readyz` reports it.
- `migrate up` applies pending migrations and exits 0 (no pending = success); on a dirty version it exits 1 with a message pointing to `migrate force`. `migrate status` prints `version`, `dirty`, `latest`.
- Logs are JSON on stdout with `time`, `level`, `msg`, and `request_id` where available.
- No `init()` side effects and no global mutable state; everything is wired in `main`.

### Out of scope
- Business modules, tables, and endpoints (identity, wallet, ledger, …).
- `platform/money`, `platform/id`, `platform/clock`, `platform/queue`, `database.InTx` — each comes with the first task that needs it.
- sqlc setup (first task with queries adds `sqlc.yaml` and the CI check for generated code).
- OpenAPI file `api/openapi/wallet.yaml` (comes with the first public endpoint task).
- Real bank simulator behaviour (separate task per TDR-008).
- Deployment beyond local docker compose.

## Acceptance Criteria
- [x] AC-1 — Given the repo is cloned, when `make ci` runs locally, then tidy-check, fmt-check, vet, lint, build, unit tests and integration tests all pass.
- [x] AC-2 — Given the compose stack is started with `make up`, when `GET /healthz` is called on wallet `:8080` and banksim `:9090`, then both return `200 {"status":"ok"}` with an `X-Request-ID` header.
- [x] AC-3 — Given Postgres is up and migrations are applied, when `GET /readyz` is called, then it returns `200` with `database:"ok"` and `migrations:"ok"`.
- [x] AC-4 — Given Postgres is up but `migrate up` has not run, when `GET /readyz` is called, then it returns `503` with `migrations:"pending"`.
- [x] AC-5 — Given the database becomes unreachable after startup, when `GET /readyz` is called, then it returns `503` with `database:"fail"` and the process keeps running.
- [x] AC-6 — Given a request with `X-Request-ID: abc`, then the response has `X-Request-ID: abc` and the access log line contains `request_id=abc`; without the header, a new UUID is generated and returned.
- [x] AC-7 — Given an unknown path / wrong method / a panicking handler, then the response is `404 NOT_FOUND` / `405 METHOD_NOT_ALLOWED` / `500 INTERNAL_ERROR` with the TDR-013 error body.
- [x] AC-8 — Given `DATABASE_URL` is missing or `LOG_LEVEL`/`SHUTDOWN_TIMEOUT` is invalid, when `wallet serve` starts, then it exits with code 1 and an error naming the variable.
- [x] AC-9 — Given `wallet serve` is running with a slow in-flight request, when SIGTERM arrives, then the request completes, new connections are refused, and the process exits 0 within `SHUTDOWN_TIMEOUT`.
- [x] AC-10 — Given `wallet foo`, then usage is printed to stderr and the exit code is 2.
- [x] AC-11 — `wallet migrate up` applies the baseline migration (version 1); running it again is a no-op with exit 0; `migrate status` prints `version=1 dirty=false latest=1`; with a dirty version `migrate up` exits 1 and `/readyz` reports `migrations:"fail"`.
- [x] AC-12 — `GET /metrics` returns Prometheus text including `go_goroutines` and `http_requests_total`.
- [x] AC-13 — `golangci-lint` fails on a `float64` inside a guarded money package and on an `internal/platform` package importing a business module (proved once with a throwaway file during implementation, then removed; result recorded in `implementation.md`).
- [x] AC-14 — The private repo `yaya1426/e-wallet` exists with `main` holding the docs/skills commit; T001 is on `task/T001-project-bootstrap`; the CI workflow runs on the PR and all four jobs (`lint`, `test`, `integration`, `smoke`) pass.
- [x] AC-15 — `main` protection requires the four checks and blocks force-push (or, if GitHub refused on the private plan, the repo was made public and protection applied).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | CI / local | `make ci` |
| AC-2 | e2e | `TestSmoke_Healthz` (wallet + banksim) |
| AC-3 | integration | `TestReadyz_MigrationsApplied_OK` (testcontainers) |
| AC-4 | integration | `TestReadyz_MigrationsPending_Unavailable` |
| AC-5 | unit + integration | `TestReadyz_DatabaseDown_Unavailable` (fake check), `TestReadyz_ContainerStopped_Unavailable` |
| AC-6 | unit | `TestRequestID_PropagatesIncoming`, `TestRequestID_GeneratesWhenMissing` |
| AC-7 | unit | `TestRouter_NotFound`, `TestRouter_MethodNotAllowed`, `TestRecover_PanicReturns500` |
| AC-8 | unit | `TestConfig_Load` table: missing `DATABASE_URL`, bad `LOG_LEVEL`, bad `SHUTDOWN_TIMEOUT`, defaults |
| AC-9 | unit | `TestServer_GracefulShutdown_FinishesInFlight` |
| AC-10 | unit | `TestCLI_UnknownCommand_Exit2` (run with args, assert exit code via a `run(args) int` function) |
| AC-11 | integration | `TestMigrate_UpIdempotent_StatusApplied`, `TestMigrate_DirtyVersion_Fails` (set `dirty=true` in `schema_migrations`) |
| AC-12 | unit | `TestMetrics_Exposed` |
| AC-13 | manual check | throwaway file + `make lint`, recorded in `implementation.md` |
| AC-14 | manual / CI | `gh repo view`, `gh pr checks` |
| AC-15 | manual | `gh api repos/yaya1426/e-wallet/branches/main/protection` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Status of the TDRs T001 depends on | Accept 4 / accept all / keep Proposed | Answered | Accept TDR-001, 002, 012, 013 |
| 2 | Repo & module path | `yaya1426/e-wallet` private / public / user creates | Answered | `yaya1426/e-wallet`, private |
| 3 | Go version | 1.25 installed / newest stable | Answered | Go 1.25 (toolchain 1.25.4) — **superseded by #13** |
| 4 | CI scope | full + compose smoke / without smoke / lint+unit | Answered | Full + compose smoke |
| 5 | Protect `main` | require CI / + 1 approval / none | Answered | Require CI, no approvals |
| 6 | How docs/skills reach `main` | direct initial commit / inside T001 PR | Answered | Direct initial commit |
| 7 | PostgreSQL version | 18 / 17 | Answered | 18 |
| 8 | Container base image | distroless static / alpine / scratch | Answered | Distroless static nonroot |
| 9 | Protection refused on private free plan | skip and note / make public / stop | Answered | Make the repo public |
| 10 | CI triggers | PRs + push to main / PRs only | Answered | PRs + push to main |
| 11 | `/healthz`, `/readyz` body | status + checks map / status code only | Answered | Status + checks map |
| 12 | Migration tool | (user instruction) | Answered | golang-migrate instead of goose (TDR-014) |
| 13 | Go 1.25 is EOL and current deps need ≥ 1.26 — which Go? | 1.27.3 / 1.26.10 / stay on 1.25 with old deps | Answered | Go 1.27.3 |
| 14 | Compose PostgreSQL host port (5432 is busy on the dev Mac) | keep 5433 / use 5432 / don't publish | Answered | Use 5432 (default of `POSTGRES_HOST_PORT`, overridable) |
| 15 | Which review round-1 findings to fix | Major+Minor / all / Major only / none | Answered | Major + Minor (#1–#9); Nits stay optional |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-03 | Accept TDR-001, TDR-002, TDR-012, TDR-013 | Yahya | the 4 TDRs, `docs/README.md` |
| 2026-11-03 | Repo `yaya1426/e-wallet` (private), module `github.com/yaya1426/e-wallet` | Yahya | TDR-012 |
| 2026-11-03 | Go 1.25 / toolchain go1.25.4 | Yahya | TDR-012 |
| 2026-11-03 | Go 1.27 / toolchain go1.27.3 (replaces 1.25: EOL, deps need ≥ 1.26) — asked during implementation | Yahya | TDR-012, this task |
| 2026-11-03 | Branch protection refused on the private free plan → repo made **public** (confirmed again by the user at that moment), then protection applied | Yahya | this task |
| 2026-11-03 | Compose publishes PostgreSQL on host port 5432 (`POSTGRES_HOST_PORT` overrides it) | Yahya (review round 1) | this task, README, `.env.example` |
| 2026-11-03 | CI: lint, test, integration, compose smoke; on PRs and push to main | Yahya | TDR-012 |
| 2026-11-03 | Protect `main`: PR + green checks, no force-push, no required approvals; if refused on private free plan → make repo public | Yahya | TDR-012, this task |
| 2026-11-03 | docs + skills land as a direct first commit on `main` | Yahya | this task |
| 2026-11-03 | PostgreSQL 18 (`postgres:18-alpine`) | Yahya | TDR-002, architecture.md |
| 2026-11-03 | Distroless static nonroot images, `time/tzdata` embedded | Yahya | TDR-012 |
| 2026-11-03 | Health bodies: status + checks map | Yahya | this task |
| 2026-11-03 | Use golang-migrate instead of goose | Yahya | new TDR-014; TDR-002, TDR-012 marked superseded for migrations; implement-task skill; review checklist |
| 2026-11-03 | CLI uses stdlib `flag` (no framework), follows TDR-012 "small dependency set" | planning (from TDR-012) | TDR-012 |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-03 | Draft | Created by plan-new-task |
| 2026-11-03 | Ready | 12 questions answered (incl. golang-migrate); doc changes applied |
| 2026-11-03 | In Progress | implement-task started; repo created (private), main pushed, branch task/T001-project-bootstrap |
| 2026-11-03 | In Review | Implemented and verified locally (lint 0 issues, 60 tests pass, compose + HTTP checks); AC-14/15 complete in finalize-task. See implementation.md |
| 2026-11-03 | Changes Requested | Review round 1: 2 Major, 7 Minor, 6 Nit — see review.md |
| 2026-11-03 | Approved | Review round 2: findings #1–#9 fixed and re-verified (65 tests, lint 0); Nits optional |
| 2026-11-03 | Done | Finalized — PR opened |
| 2026-11-03 | Done | PR yaya1426/e-wallet#1 merged (fb70478) after lint/test/integration/smoke passed; repo made public; `main` protected (PR + 4 checks, strict, no force-push/deletion, no approvals, admins included) |
| 2026-11-04 | Done | Note: Dockerfile and docker-compose.yml later moved to the repo root (TDR-015) |

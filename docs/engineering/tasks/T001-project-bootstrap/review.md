# Review — T001

## Round 2 (2026-11-03)

Verdict: **Approved**

Summary: Every Major and Minor finding from round 1 (#1–#9) is fixed and has a test or a
live check. Static state is clean again: gofmt clean, vet ok, golangci-lint 0 issues, and 65 tests
pass with `-race` (unit + integration). The compose stack, e2e and the live checks pass. The user
resolved the open decision: PostgreSQL host port **5432**. Nits #10–#15 are still open as optional, by the user's choice.

| # | Round-1 severity | Status | Evidence |
|---|------------------|--------|----------|
| 1 | Major | **Fixed** | `methodLabel` in `httpserver/middleware.go` maps non-standard methods to `OTHER`; `TestAccessLog_NonStandardMethodObservedAsOther`; live: `FOO1..3` → one `method="OTHER"` series |
| 2 | Major | **Fixed** | `Migrator.Up` returns `migrate up interrupted: context canceled` when ctx ends; `TestMigrate_CancelledContext_ReportsInterrupted` |
| 3 | Minor | **Fixed** | `stop_grace_period: 30s` on `wallet`; live SIGTERM → exit 0 |
| 4 | Minor | **Fixed** | `srv.Close()` after a failed `Shutdown`; `TestServer_ShutdownTimeoutExceeded` now asserts the stuck connection is closed |
| 5 | Minor | **Fixed** | `make migrate` builds `wallet` first; ran it live → "migrations applied" |
| 6 | Minor | **Fixed** | `config.parse` joins every error, each naming its variable; `TestConfig_LoadWallet_ReportsEveryInvalidVariable` |
| 7 | Minor | **Fixed** | `readinessChecks` logs `readiness check failed` with check + error at warn; live with postgres stopped |
| 8 | Minor | **Fixed** | Deviation #6 added to `implementation.md` |
| 9 | Minor | **Fixed** | `TestServe_CancelledContext_ExitsZero` (integration), banksim `TestRun_CancelledContext_ExitsZero` |
| 10–15 | Nit | Open (optional) | Not applied, by user decision |
| — | Decision | **Resolved** | Host port 5432 (default of `POSTGRES_HOST_PORT`); Open Question #14 |

New code from the fixes was reviewed against the same checklist: no new findings.

## Round 1 (2026-11-03)

Verdict: **Changes Requested**

Summary: The skeleton is clean, follows TDR-001/012/013/014, and the static state is green
(gofmt clean, `go vet` ok, golangci-lint 0 issues; import graph matches architecture §3.2). There are
two **Major** issues: an unbounded Prometheus label (the HTTP method comes straight from the
client), and `wallet migrate up` reporting success after an interrupted run. Both are small,
local fixes. The rest are Minor or Nit.

Method: I read every changed file in full against the go-review checklist, ran an independent
second review with a subagent on the same files, merged the findings, and checked each one
against the code or library source. Its finding about the CI golangci-lint binary was checked
and dropped (the v2.15.0 release binary is built with go1.27.0).

## Findings

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | **Major** | `internal/platform/metrics/metrics.go:51`, `internal/platform/httpserver/middleware.go:78` | Security / unbounded resource use (checklist C "no unbounded …", gosec-class DoS) | `ObserveHTTP(r.Method, …)` uses the raw request method as a label. net/http accepts any token as a method, so `FOO1`, `FOO2`, … each create new `http_requests_total` and histogram series. Memory and `/metrics` size grow without limit. | Normalise the method to the standard set (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE), anything else to `OTHER`, before observing (in the middleware, which owns the HTTP knowledge). Add a test. |
| 2 | **Major** | `internal/platform/database/migrate.go:61-80` | Error handling (checklist C, errors) | When ctx is cancelled, `GracefulStop` is sent and golang-migrate v4.20.1 returns `nil` from `Up()` (`migrate.go:726` `if m.stop() { return nil }`). So SIGTERM during `wallet migrate up` exits 0 and logs "migrations applied" with migrations still pending. Compose's `service_completed_successfully` then starts `wallet` on an unfinished schema. | After `runner.Up()`, if `ctx.Err() != nil`, return `fmt.Errorf("database: migrate up interrupted: %w", ctx.Err())`. Add a unit/integration test with a cancelled context. |
| 3 | Minor | `deployments/docker-compose.yml` (wallet service) | PRD-001 NFR-10 graceful shutdown | No `stop_grace_period`. Docker sends SIGKILL after 10 s, but `SHUTDOWN_TIMEOUT` is 25 s, so `docker compose stop`/`make down` can kill requests the app is still allowed to finish. | `stop_grace_period: 30s` on `wallet`. |
| 4 | Minor | `internal/platform/httpserver/server.go:75-77` | Shutdown behaviour | When `Shutdown` hits the timeout, the error is returned without `s.srv.Close()`, so still-active connections stay open. The caller then runs `pool.Close()`, which waits for acquired connections, so a stuck handler could keep the process alive past the timeout. | On a `Shutdown` error, call `_ = s.srv.Close()` and return the error. Extend `TestServer_ShutdownTimeoutExceeded` to assert the connection is closed. |
| 5 | Minor | `Makefile:58-59` | Developer ergonomics / task How (`make migrate`) | `make migrate` → `compose run --rm migrate` fails on a fresh clone: `migrate` has `pull_policy: never` and its image is built only by the `wallet` service. | `$(COMPOSE) build wallet` before `run --rm migrate`. |
| 6 | Minor | `internal/platform/config/config.go:89-95` | Error handling | When several env vars are wrong, `errors.As` picks the first `ParseError` and only that message is returned. Other problems (e.g. a missing `DATABASE_URL`) are hidden until the next start. | Map every error in `env.AggregateError.Errors` (naming each variable) and `errors.Join` them. Add a test case with two bad variables. |
| 7 | Minor | `cmd/wallet/app.go:140-160` | Observability (NFR-7) | Readiness checks drop the underlying error. A `fail` in `/readyz` cannot be diagnosed from the logs. | Pass the logger into `readinessChecks` and log the error at `warn` with the check name. |
| 8 | Minor | `docs/engineering/tasks/T001-project-bootstrap/implementation.md` (Deviations) vs task How | Plan conformance | The task named `MigrateUp`, `MigrationStatus` and `MigrationState(ctx)` returning ok/pending/fail, plus `database.Ping`. The code has `Migrator.Up`, `Migrator.Status` + `MigrationStatus.Pending()`, maps to ok/pending/fail in `cmd/wallet` (so `database` doesn't know HTTP check vocabulary), and uses `pool.Ping` directly. The behaviour is the same, but this deviation isn't listed. | Add it to the Deviations section (no code change). |
| 9 | Minor | `cmd/wallet/main_test.go`, `cmd/banksim` | Tests (checklist E) | AC-9 ("process exits 0") is tested at server level and by hand with compose, but no test checks that `run(ctx, ["serve"], …)` returns `exitOK` after cancel. Banksim has no shutdown test. | Add a `serve` test using a testcontainers DB + `HTTP_ADDR=127.0.0.1:0` that cancels ctx and asserts exit 0. Add a similar test for banksim (no DB needed). |
| 10 | Nit | `cmd/wallet/main.go:34-37` | Go idiom | `NotifyContext` keeps catching signals until `run` returns, so a second Ctrl-C cannot force-quit during shutdown. | Call `stop()` when `ctx.Done()` fires (e.g. `context.AfterFunc(ctx, stop)`). |
| 11 | Nit | `cmd/banksim/main.go:39,46` | Consistency | Literal exit codes `1`/`0`, while `cmd/wallet` uses named constants. | Use named constants. |
| 12 | Nit | `test/e2e/smoke_test.go:62` | Errors | `req, _ := http.NewRequestWithContext(…)` ignores the error. | `require.NoError`. |
| 13 | Nit | `internal/platform/httpserver/handler.go:21-26` | Efficiency | Routed requests are looked up twice (`mux.Handler` then `mux.ServeHTTP`). This is negligible now. | Keep it, or add a comment that this is deliberate so `r.Pattern` is set by `ServeHTTP`. |
| 14 | Nit | `internal/platform/httpserver/middleware.go:105-114` | HTTP edge case | If a handler panics after writing headers, `WriteError` writes a second header ("superfluous WriteHeader" log) and appends to a partial body. | Skip `WriteError` when `statusRecorder` already wrote headers; log only. |
| 15 | Nit | `deployments/Dockerfile:14`, `.dockerignore` | Hygiene | `USER nonroot:nonroot` is redundant on a `:nonroot` image. `.dockerignore` excludes `.env` but not `.env.*`. | Drop the line (or keep it as documentation); add `.env.*`. |

### Needs a decision from the user (not a code defect)
- **PostgreSQL host port 5433** (`deployments/docker-compose.yml`, `POSTGRES_HOST_PORT`): this changes
  the planned compose setup and was recorded as a deviation in `implementation.md`. Per implement-task,
  deviations need your approval.

## Checked and fine
- **A Architecture:** layout matches TDR-012. `go list` import graph: `cmd/*` → `internal/platform/*`;
  `platform/httpserver` → `platform/logger`; `platform/database` → `migrations`. No business module exists yet, and depguard
  is proven to block platform → business imports. No abstraction leakage: pgx stays inside `database` and `cmd/wallet`,
  and HTTP vocabulary stays inside `httpserver`. `Observer` interface is defined by its consumer.
- **B Money:** nothing yet. forbidigo is proven to apply only to the money packages.
- **C Go standards:**
  - Naming is idiomatic (`ErrDirty`, `MigrationStatus`, `CheckStatus`, short receivers, no stutter), and every exported identifier has a doc comment.
  - Errors are wrapped with `%w`, have lower-case prefixes and are checked with `errors.Is/As`.
  - ctx is the first parameter and is never stored. Goroutines all have an owner and a stop path; `serveErr` is buffered.
  - No `init()` and no global mutable state in production code.
  - Request-ID validation blocks header and log injection, and panics are logged without leaking to the client.
- **D Data & API:** golang-migrate file pair naming per TDR-014, migrations embedded, status read-only (no table created by `/readyz`), dirty/NilVersion → `fail`, `ErrNoChange` = success; health bodies, error body, 404/405/500 codes, `X-Request-ID` per TDR-013 and the task.
- **E Tests:** every AC-1…AC-13 has automated tests (60 passing with `-race`), deterministic (no sleeps for synchronisation except the deliberate slow handler), integration tests use testcontainers `postgres:18-alpine`.
- **Infra:** Dockerfile distroless nonroot, `CGO_ENABLED=0`, `-trimpath`; compose healthcheck and ordering; PG18 volume path; CI triggers, job names, `concurrency`, logs on failure, `down` always; no secrets in `.env.example`; `time/tzdata` in both mains.

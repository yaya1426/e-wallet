# T001 — Implementation Report

| Field   | Value |
|---------|-------|
| Date    | 2026-11-03 |
| Branch  | `task/T001-project-bootstrap` (not committed yet — finalize-task commits) |
| Result  | All local verification steps pass; CI run and branch protection happen in `/finalize-task T001` |

## Summary
- Git repo initialised; author set **for this repo only** to
  `Yahya Elaraby <9460976+yaya1426@users.noreply.github.com>` (user decision; no global git
  identity existed). First commit `8b22ac1 docs: initial PRDs, TDRs, architecture and task
  workflow skills` on `main`; private repo **github.com/yaya1426/e-wallet** created and `main` pushed.
- Go module `github.com/yaya1426/e-wallet`, **Go 1.27 / toolchain go1.27.3** (changed from 1.25
  with the user's approval, see Deviations).
- `cmd/wallet` (`serve` default, `migrate up`, `migrate status`, stdlib `flag`), `cmd/banksim`
  (`/healthz` only).
- `internal/platform/{config,logger,database,httpserver,metrics}` +
  `internal/platform/database/databasetest` (integration-test helper, build tag `integration`).
- golang-migrate (TDR-014) with embedded `migrations/000001_baseline.{up,down}.sql`.
- Makefile, `.golangci.yml` (v2), `deployments/Dockerfile` (distroless static nonroot),
  `deployments/docker-compose.yml`, `.dockerignore`, `configs/.env.example`, `.gitignore`,
  `README.md`, `.github/workflows/ci.yml`, `test/e2e/smoke_test.go`.

### Files
| Path | Notes |
|------|-------|
| `cmd/wallet/main.go` | CLI dispatch, exit codes 0/1/2 |
| `cmd/wallet/app.go` | `serve`, `migrateUp`, `migrateStatus`, `newHandler`, `readinessChecks` (composition root) |
| `cmd/banksim/main.go` | Stub server |
| `internal/platform/config/config.go` | `LoadWallet`, `LoadBanksim`; every error names the variable |
| `internal/platform/logger/logger.go` | slog JSON; `request_id` added from context |
| `internal/platform/database/database.go` | `Open` (pool + ping) |
| `internal/platform/database/migrate.go` | `Migrator.Up`, `Migrator.Status`, `ErrDirty`; status is read-only (no table creation on `/readyz`) |
| `internal/platform/httpserver/*.go` | `Server` (graceful shutdown), middleware (request ID, access log + observer, panic recovery), JSON 404/405 fallback, `WriteError`/`WriteJSON`, `Healthz`/`Readyz` |
| `internal/platform/metrics/metrics.go` | Registry, Go/process collectors, `http_requests_total`, `http_request_duration_seconds` |
| `migrations/embed.go`, `migrations/000001_baseline.*.sql` | Embedded migrations |

### Dependencies (direct)
`caarlos0/env/v11 v11.4.2`, `jackc/pgx/v5 v5.12.0`, `golang-migrate/migrate/v4 v4.20.1`,
`prometheus/client_golang v1.25.0`, `google/uuid v1.6.0`, `golang.org/x/sync v0.24.0`,
`stretchr/testify v1.12.1`, `testcontainers-go v0.44.0` (+ `modules/postgres`).
Tools pinned in the Makefile/CI: golangci-lint **v2.15.0**, migrate CLI **v4.20.1**
(`make tools` installs both into `./bin`, git-ignored).

## Acceptance Criteria → Tests
| AC | Test(s) | Result |
|----|---------|--------|
| AC-1 `make ci` passes | `make ci` (tidy-check, fmt-check, vet, lint, build, unit, integration) | ✅ |
| AC-2 healthz wallet + banksim | `TestSmoke_Healthz` (e2e), `TestHealthz_RouteServed`, banksim `TestHealthz_OK`, curl | ✅ |
| AC-3 readyz ok after migrate | `TestReadyz_MigrationsApplied_OK` (integration), `TestSmoke_Readyz`, curl | ✅ |
| AC-4 readyz pending before migrate | `TestReadyz_MigrationsPending_Unavailable` (integration) | ✅ |
| AC-5 DB down → 503, process keeps running | `TestReadyz` "database down" (unit), `TestReadyz_ContainerStopped_Unavailable` (integration), compose: stop postgres | ✅ |
| AC-6 X-Request-ID propagated / generated, in access log | `TestRequestID_PropagatesIncoming`, `TestRequestID_GeneratesWhenMissing`, `TestRequestID_ReplacesInvalidIncoming`, curl + compose logs | ✅ |
| AC-7 404 / 405 / 500 error body | `TestRouter_NotFound`, `TestRouter_MethodNotAllowed`, `TestRecover_PanicReturns500`, `TestRecover_ReraisesAbortHandler`, `TestSmoke_NotFound`, curl | ✅ |
| AC-8 bad config → exit 1 naming the variable | `TestConfig_LoadWallet_InvalidValuesNameTheVariable`, `TestServe_InvalidConfig_Exit1NamesVariable`, `TestServe_DatabaseUnreachable_Exit1` | ✅ |
| AC-9 graceful shutdown | `TestServer_GracefulShutdown_FinishesInFlight`, `TestServer_ShutdownTimeoutExceeded`, compose: `docker compose stop wallet` → exit 0 | ✅ |
| AC-10 unknown command → exit 2 | `TestCLI_UnknownCommand_Exit2`, `TestCLI_UsageErrors_Exit2`, `TestCLI_Help_Exit0` | ✅ |
| AC-11 migrate up idempotent, status, dirty | `TestMigrate_UpIdempotent_StatusApplied`, `TestMigrate_DirtyVersion_Fails`, `TestMigrateCommands_UpIdempotentAndStatus`, `TestMigrateUp_DirtyVersion_Exit1`, `TestReadyz_DirtyMigration_Fail` | ✅ |
| AC-12 /metrics | `TestMetrics_Exposed` (metrics + cmd/wallet), `TestSmoke_Metrics`, curl | ✅ |
| AC-13 lint guards | Throwaway probe files (see below) | ✅ |
| AC-14 repo + CI green on the PR | Repo exists (private), `main` has the docs commit, branch created. **CI has not run yet** — it runs when finalize-task opens the PR | ⏳ finalize-task |
| AC-15 branch protection | Done by finalize-task after the first CI run (task How, step 4) | ⏳ finalize-task |

## Verification
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `go mod tidy -diff` | ✅ no diff |
| 2 | Format | `gofmt -l .` (+ goimports via golangci formatters) | ✅ nothing listed |
| 3 | Vet | `go vet -tags 'integration e2e' ./...` | ✅ |
| 4 | Lint | `bin/golangci-lint run ./...` (v2.15.0) | ✅ 0 issues |
| 5 | Build | `go build ./...` | ✅ |
| 6 | Unit tests | `go test -race -count=1 -cover ./...` | ✅ 50 passed, 0 failed, 0 skipped |
| 7 | Integration tests | `go test -race -count=1 -tags integration ./...` (testcontainers `postgres:18-alpine`) | ✅ 60 passed (unit + integration), 0 failed, 0 skipped |
| 8 | Coverage (unit + integration) | `-tags integration -cover` | httpserver 93.6%, metrics 100%, logger 88.9%, config 86.0%, cmd/wallet 78.6%, database 71.6%, cmd/banksim 41.2% (only `main`/`run` startup not covered) |
| 9 | Run the service | `make up` (`docker compose up -d --build --wait`) | ✅ postgres healthy, migrate exited 0, wallet + banksim running |
| 10 | HTTP checks | curl, see below; `make e2e` | ✅ e2e `ok` |
| 11 | Ledger integrity | — | n/a: no ledger exists yet |
| 12 | Tear down | `make down` | ✅ |
| — | `make ci` end to end | | ✅ |

### AC-13 lint guard proof
Added `internal/ledger/probe.go` (returns a `float64`) and `internal/platform/zzprobe/probe.go`
(imports `internal/ledger`), ran lint, then deleted both:
```
internal/platform/zzprobe/probe.go:4:8: import 'github.com/yaya1426/e-wallet/internal/ledger' is not allowed from list 'platform-no-business': platform must not import business modules (depguard)
internal/ledger/probe.go:5:16: use of `float64` forbidden because "money is int64 piasters; floats are forbidden here (TDR-003)" (forbidigo)
2 issues
```
The `float64` in `internal/platform/zzprobe` was **not** reported, which confirms that forbidigo applies only to the money packages. After deleting the probes: `0 issues`.

### HTTP checks (compose stack)
| Request | Expected | Actual |
|---------|----------|--------|
| `GET :8080/healthz` | 200 `{"status":"ok"}` + X-Request-ID | ✅ 200, UUIDv7 request ID |
| `GET :9090/healthz` (banksim) | 200 `{"status":"ok"}` | ✅ |
| `GET :8080/readyz` | 200, database ok, migrations ok | ✅ `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}` |
| `GET /healthz` with `X-Request-ID: abc` | header echoed | ✅ `X-Request-Id: abc` |
| `GET /nope` with `X-Request-ID: abc` | 404 + access log with request_id | ✅ log line `"status":404,…,"request_id":"abc"` |
| `GET /nope` | 404 `NOT_FOUND` body | ✅ `{"error":{"code":"NOT_FOUND","message":"Resource not found.","request_id":"…"}}` |
| `POST /healthz` | 405 `METHOD_NOT_ALLOWED` + Allow | ✅ `Allow: GET, HEAD` |
| `GET /metrics` | go_goroutines, http_requests_total | ✅ |
| `docker compose stop postgres` → `GET /readyz` | 503, database fail, wallet still running | ✅ `{"status":"unavailable","checks":{"database":"fail","migrations":"fail"}}`, container running |
| `docker compose start postgres` → `GET /readyz` | back to 200 | ✅ |
| `wallet migrate status` / `migrate up` again | version=1, no-op | ✅ `version=1 dirty=false latest=1`, `migrations applied` |
| `docker compose stop wallet` (SIGTERM) | graceful, exit 0 | ✅ "http server shutting down" → "http server stopped", exit 0 |

## Deviations from the plan
1. **Go 1.27.3 instead of 1.25** — asked the user during implementation (Go 1.25 is EOL and
   current dependencies need ≥ 1.26). **Approved**; TDR-012 and task.md updated (Open Question #13).
2. **Repo-local git author** — no git identity was configured; the user chose the GitHub noreply address.
3. **PostgreSQL host port** — first published on `5433`; in review the user chose **5432**
   (default of `POSTGRES_HOST_PORT`, which can override it). On this Mac 5432 is taken by another
   container, so local verification runs with `POSTGRES_HOST_PORT=5433 make up`.
4. **`migrate` compose service reuses the `wallet` image** (`pull_policy: never`) instead of
   building it too: two services building the same tag in parallel failed with "image already exists".
5. With the database down, `/readyz` reports `migrations: "fail"` too (status can't be read).
   This follows the task's rule (`fail` when the DB can't be read); the task's example only showed the shape.
6. **Migration API shape** — the task named `MigrateUp`, `MigrationStatus`, `MigrationState(ctx)`
   returning ok/pending/fail, and `database.Ping`. Implemented as `Migrator.Up`, `Migrator.Status`
   (returns `MigrationStatus` with `Pending()`), with the ok/pending/fail mapping in
   `cmd/wallet/app.go` `readinessChecks` so `database` does not depend on the HTTP check
   vocabulary, and `pool.Ping` used directly. Same behaviour; recorded per review finding #8.
7. Added an integration-test helper package `internal/platform/database/databasetest` and a few
   extra tests (shutdown-timeout, abort-handler re-panic, invalid incoming request IDs, `-h`).
   No behaviour beyond the task.

## Review fixes — re-verification (round 2)
Fixed review findings #1–#9 (see `review.md`). Full re-run:
| Step | Result |
|------|--------|
| `make ci` (tidy, fmt, vet, lint, build, unit, integration) | ✅ exit 0, lint 0 issues |
| Tests (unit + integration, `-race`) | ✅ 65 passed, 0 failed, 0 skipped |
| Coverage | httpserver 93.8%, metrics 100%, logger 88.9%, cmd/wallet 86.2%, config 86.0%, database 73.7%, cmd/banksim 64.7% |
| `POSTGRES_HOST_PORT=5433 make up` + `make e2e` | ✅ (5432 is taken on this Mac; the default is 5432) |
| Live: methods `FOO1..3` → `/metrics` | ✅ one `method="OTHER"` series |
| Live: `make migrate` | ✅ "migrations applied" |
| Live: postgres stopped → `/readyz` 503 + `readiness check failed` warn logs with cause | ✅ |
| Live: SIGTERM wallet (`stop_grace_period: 30s`) | ✅ exit 0 |

## Not verified here
- The GitHub Actions workflow has not run yet (first run is on the PR). Its YAML is not
  validated by a linter locally (actionlint is not installed).
- Branch protection (finalize-task).

## Follow-ups (not done)
- Consider adding `actionlint` to `make lint` / CI once workflows grow.

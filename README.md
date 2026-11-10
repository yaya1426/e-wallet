# E-Wallet

An EGP e-wallet backend in Go: top-up, withdrawal and peer-to-peer transfers on top of a
double-entry ledger in PostgreSQL. One Go service (modular monolith) plus a bank simulator
for development.

All product and technical decisions live in [`docs/`](docs/README.md) — start there.
Work is done task by task (`docs/engineering/tasks/`) with the project skills in
`.claude/skills/` (plan → implement → review → finalize).

## Prerequisites

- Go (version pinned in `go.mod`; the `go` command downloads the right toolchain itself)
- Docker with Docker Compose (local stack and integration tests)
- `make`

Install the pinned developer tools (golangci-lint, migrate CLI) into `./bin`:

```bash
make tools
```

## Run locally

```bash
make up
```

This starts PostgreSQL 18 (host port `5432`, override with `POSTGRES_HOST_PORT`), runs `wallet migrate up` once, then starts the
wallet on `:8080` and the bank simulator on `:9090`.

```bash
curl -i localhost:8080/readyz
```

```bash
make down
```

To run the wallet outside Docker against the compose database, use the values in
`configs/.env.example` and run `go run ./cmd/wallet serve`.

## Try it

[`docs/engineering/manual-testing.md`](docs/engineering/manual-testing.md) walks through
top-up, P2P and withdrawal against the bank simulator with `curl` and `jq`. The same scenarios
run automatically with `make up && make e2e` (CI runs them in the `smoke` job). If PostgreSQL
is not on host port 5432, set `E2E_DATABASE_URL` for `make e2e`.

## Commands

| Command | Purpose |
|---------|---------|
| `wallet serve` (default) | Run the HTTP API |
| `wallet migrate up` | Apply pending migrations (the service never migrates by itself) |
| `wallet migrate status` | Print `version`, `dirty`, `latest` |
| `banksim` | Bank simulator (dev/test only) |

## Make targets

`make help` lists them all. The main ones:

| Target | Purpose |
|--------|---------|
| `make ci` | tidy-check, fmt-check, vet, lint, build, unit and integration tests |
| `make test` | Unit tests with `-race` |
| `make test-integration` | Integration tests against PostgreSQL in testcontainers |
| `make e2e` | Smoke tests against the running compose stack |
| `make migration name=<snake_name>` | Create a new `NNNNNN_name.up.sql` / `.down.sql` pair |

## Migrations

SQL files in `migrations/` managed with golang-migrate ([TDR-014](docs/tdrs/TDR-014-golang-migrate.md)).
If a migration fails half-way, the version is marked **dirty**: `/readyz` reports
`migrations: "fail"` and `wallet migrate up` refuses to run. Repair the schema by hand, then
mark the right version with the migrate CLI:

```bash
bin/migrate -path migrations -database "$DATABASE_URL" force <version>
```

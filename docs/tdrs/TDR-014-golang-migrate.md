# TDR-014 — Database Migrations with golang-migrate

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-03, during T001 planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | Supersedes the migration-tool parts of TDR-002 and TDR-012 (goose); T001 |

## Context
TDR-002 and TDR-012 picked `pressly/goose` for migrations. While planning T001 the user
decided to use **golang-migrate** instead. Everything else in those TDRs stays as it is.

## Decision
- Library: **`github.com/golang-migrate/migrate/v4`**, used as a Go library inside the
  `wallet` binary (no separate CLI needed to run the service):
  - source driver `source/iofs`, reading SQL files embedded with `//go:embed` from `migrations/`;
  - database driver `database/pgx/v5` (same driver family as the app, TDR-012).
- Files: `migrations/NNNNNN_<snake_name>.up.sql` and `.down.sql`, 6-digit sequential versions
  (`000001_baseline.up.sql`). Every migration has both files; `down` is for local dev only
  (production is forward-only, as in TDR-002).
- Version table: golang-migrate's default `schema_migrations (version, dirty)`.
- Commands: `wallet migrate up` (apply all pending), `wallet migrate status` (print current
  version, dirty flag and the latest embedded version). `migrate down` is not exposed in the
  binary; local rollbacks use the `migrate` CLI via `make migrate-down`.
- A **dirty** version (a migration failed half-way) is treated as an error: `/readyz` reports
  `migrations: "fail"` and `migrate up` refuses to continue until fixed by hand (`migrate force`).
- New migration files are created with the `migrate` CLI (`make migration name=<snake_name>` →
  `migrate create -ext sql -dir migrations -seq -digits 6 <name>`); the CLI is installed by `make tools`.
- `wallet serve` never migrates (unchanged from TDR-002).

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| `pressly/goose` (TDR-002/012) | Replaced by user decision; golang-migrate is the more widely used option and keeps up/down as separate plain SQL files. |
| Atlas | Declarative schema diffing; more than we need. |

## Consequences
- + Plain SQL, explicit up/down files, a well-known tool.
- − No Go-code migrations (not needed; all migrations are SQL).
- − The `dirty` state needs manual repair after a failed migration (documented in the README).

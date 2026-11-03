# Verification procedure (shared by implement-task and finalize-task)

Run from the repository root. Prefer `make` targets when the Makefile defines them
(`make fmt-check lint test test-integration build e2e`); otherwise use the raw commands.
**Every step must pass.** Record each command, its result (pass/fail) and short output in the
report.

| # | Step | Command | Pass when |
|---|------|---------|-----------|
| 1 | Dependencies tidy | `go mod tidy && git diff --exit-code go.mod go.sum` | no diff |
| 2 | Formatting | `gofmt -l .` (and `goimports -l .` if installed) | prints nothing |
| 3 | Vet | `go vet ./...` | exit 0 |
| 4 | Lint | `golangci-lint run ./...` | exit 0, no issues |
| 5 | Build | `go build ./...` | exit 0 |
| 6 | Unit tests | `go test -race -count=1 ./...` | all pass |
| 7 | Integration tests | `go test -race -count=1 -tags=integration ./...` (testcontainers Postgres; needs Docker) | all pass |
| 8 | Coverage of changed packages | `go test -cover ./internal/<changed>/...` | report the numbers; new business logic should have its business cases covered |
| 9 | Run the service | `docker compose -f deployments/docker-compose.yml up -d --build` (or `go run ./cmd/wallet` + Postgres + `go run ./cmd/banksim`), wait for `GET /readyz` = 200 | ready |
| 10 | HTTP checks | `curl` every new/changed endpoint: happy path + each error code in the task's AC; for async flows poll `GET /v1/transactions/{id}` until terminal | responses match the AC |
| 11 | Ledger integrity | run the integrity check (`wallet reconcile --job=ledger_integrity` or the SQL in TDR-004 §6) after the HTTP checks | no discrepancy |
| 12 | Tear down | `docker compose ... down -v` | — |

Rules:
- If a tool is missing (`golangci-lint`, Docker), say so explicitly in the report — never
  report a skipped step as passed. Ask the user whether to install it or proceed without.
- Steps 9–11 apply once the service can run (after the bootstrap tasks). If the task has no
  runnable surface yet, write "not applicable — reason".
- A failing step is fixed and **all** steps are run again, not only the failing one.
- Do not weaken, skip (`t.Skip`), or delete tests to make them pass.

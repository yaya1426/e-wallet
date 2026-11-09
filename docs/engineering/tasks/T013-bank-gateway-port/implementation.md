# Implementation — T013 Bank gateway port + banksim adapter

## Summary
`internal/bank` is the wallet's only door to a bank. It holds the `Gateway` port, the request,
acceptance and operation types, and the error classes (`*RejectedError`, `ErrTemporary`,
`ErrNotFound`).

- `internal/bank/banksim` implements the port over HTTP for the simulator. It uses `net/http` only, a per-call `BANK_HTTP_TIMEOUT`, 64 KB body limits and no redirects, and maps every answer as in the task table.
- `internal/bank/banktest` is a scripted, recording fake for unit tests.
- `api/openapi/banksim.yaml` is the simulator contract that T014 implements.
- Config adds `BANK_BASE_URL` (checked by `serve` only, decision 2026-11-09) and `BANK_HTTP_TIMEOUT=5s`.

## Files
| Path | Change |
|------|--------|
| `internal/bank/gateway.go` (+ test) | Port, types, `RejectedError`, `ErrTemporary`, `ErrNotFound` |
| `internal/bank/banksim/client.go` (+ test) | HTTP adapter, error mapping, logs (`bank_op`, `idempotency_key`, `status`, `duration`), `Observer` |
| `internal/bank/banktest/fake.go` (+ test) | In-memory `Gateway`: idempotent by key, `Script(key, …)`, `SetOperation`, `Calls()` |
| `api/openapi/banksim.yaml`, `api/openapi/banksim_test.go` | Contract + OpenAPI 3.1 lint |
| `internal/platform/config` | `BANK_BASE_URL` (required by `ValidateServe`, absolute http(s) URL), `BANK_HTTP_TIMEOUT` (> 0) |
| `internal/platform/metrics` | `bank_call_duration_seconds{op,result}` + `ObserveBankCall` |
| `docker-compose.yml`, `configs/.env.example` | `BANK_BASE_URL` (compose: `http://banksim:9090`) |
| `cmd/wallet/*_test.go` | serve tests pass a `BANK_BASE_URL`; new case "serve, no BANK_BASE_URL" |
| `.golangci.yml` | depguard `bank-deps` (probed: importing `wallet` fails lint) |
| DigitalOcean app (doctl, user-approved) | `BANK_BASE_URL=http://banksim:9090` (RUN_TIME) on the `e-wallet` service |

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestClient_CreateDebit`, `TestClient_CreatePayout` (method, path, `Idempotency-Key`, JSON body, `bank_reference`), `TestClient_BaseURLWithPath` | ✅ |
| AC-2 | `TestClient_ErrorMapping` (404 per op, 408, 429, 422 with code, 400 empty, 409 non-JSON, 500, 503, unparsable 2xx, 202 without reference, unexpected 200, 302), `TestClient_ConnectionErrorIsTemporary`, `TestClient_OversizedBodyIsTemporary`, `TestClient_GetOperation_InvalidBodiesAreTemporary` (USD, unknown kind/status, missing fields, bad amount) | ✅ |
| AC-3 | `TestClient_Timeout` (200 ms timeout → `ErrTemporary` in [200 ms, 1 s)), `TestClient_CallerContextCanceled` | ✅ |
| AC-4 | `TestClient_GetOperation` (every field; amount as `money.Money`) | ✅ |
| AC-5 | `TestClient_Metrics` (op/result labels for ok, rejected, temporary, not_found; log fields), `TestMetrics_Bank` | ✅ |
| AC-6 | `TestConfig_Bank`, `TestServe_InvalidConfig_Exit1NamesVariable` ("serve, no BANK_BASE_URL") | ✅ |
| AC-7 | `TestBanksimSpec_IsValidOpenAPI31` (runs in `make test` / CI) | ✅ |

The fake has its own tests (`TestFake_*`).

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`, unit + integration) | ✅ 450 passed; `internal/bank/...` 3× `-race` stable |
| Coverage | `bank/banksim` 95.7%, `bank/banktest` 95.6% |
| `make up` + `make e2e` | ✅; `serve` starts with `BANK_BASE_URL` from compose, `/readyz` 200 |
| HTTP against the real simulator | Not possible yet: `cmd/banksim` implements the contract in T014 |

## Deviations from the plan
- `BANK_BASE_URL` is required by `serve` only (answered question 2), not by every command.
- An `Operation` whose kind or status is not in the contract, or that misses its key or
  reference, is also `ErrTemporary`, like a non-EGP currency (the task named only the currency).
- The adapter is not wired into `cmd/wallet` yet; its first caller is T017.
- The contract does not define what happens when a key is reused with a different body; T014
  decides that.

## Follow-ups
- After T014, point the DigitalOcean `BANK_BASE_URL` at the deployed simulator (today it is a placeholder).

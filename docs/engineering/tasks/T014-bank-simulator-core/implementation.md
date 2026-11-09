# Implementation — T014 Bank simulator core

## Summary
`cmd/banksim` is now an in-memory fake bank (`internal/banksim`).
- It implements `api/openapi/banksim.yaml`: idempotent debits and payouts, and status queries.
- It supports the phase-1 scenarios by IBAN suffix (`0000` success, `0001` fail later, `0002` reject now) and a one-shot `POST /admin/scenarios` override.
- After `BANKSIM_WEBHOOK_DELAY` it settles the operation, then POSTs an HMAC-signed event to `BANKSIM_WEBHOOK_URL`. The event is retried after 1/2/4/8/16 s, re-signed each time, with the same `event_id`.
- It imports no wallet code; depguard enforces this.

## Files
| Path | Change |
|------|--------|
| `internal/banksim/server.go` | Routes, create (validate → replay or `IDEMPOTENCY_KEY_REUSED` → scenario → 202/422), get operation, admin override |
| `internal/banksim/store.go` | Mutex-protected maps: answers and operations by key, one-shot overrides by IBAN |
| `internal/banksim/scenario.go` | Scenario choice (override first, else suffix; other suffixes = `0000`), failure codes |
| `internal/banksim/notifier.go` | Delay → settle (status stored before the first attempt) → signed delivery with retries; stops with ctx; `Wait` |
| `internal/banksim/signature.go` | `Sign(secret, t, body)` → `t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>` |
| `cmd/banksim/main.go` (+ test) | Wires config, simulator and server; waits for background work on shutdown |
| `internal/platform/config` | `BANKSIM_WEBHOOK_URL` (required, absolute http(s)), `BANK_WEBHOOK_SECRET` (required, ≥ 32 bytes), `BANKSIM_WEBHOOK_DELAY=2s` (≥ 0); a shared `checkHTTPURL` |
| `api/openapi/banksim.yaml` (+ test) | Scenarios, `/admin/scenarios`, `400 INVALID_REQUEST`, `422 IDEMPOTENCY_KEY_REUSED`; error-code catalogue test |
| `docker-compose.yml`, `configs/.env.example` | Webhook URL/delay for banksim; a dev webhook secret shared with the wallet |
| `.golangci.yml` | depguard `banksim-deps`: no wallet module and no `internal/bank[/...]` (probed with `bank`, `bank/banktest`, `bank/banksim`, `wallet`: all fail lint) |

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1, AC-2 | `TestBanksim_Scenarios` (debit/payout × 0000/0001 + another suffix: 202 `BSIM-…`, PENDING until the delay, one webhook with every field and a valid signature, operation SUCCEEDED/FAILED with the listed codes) | ✅ |
| AC-3 | `TestBanksim_InvalidAccount` (422 twice, 404 operation, no webhook) | ✅ |
| AC-4 | `TestBanksim_Idempotency` (same body → same response, one webhook; other amount or other kind → `IDEMPOTENCY_KEY_REUSED`) | ✅ |
| AC-5 | `TestBanksim_AdminOverrideOneShot` (0001 once, then the suffix again; 0002 override; invalid override bodies → 400) | ✅ |
| AC-6 | `TestNotifier_Retries` (500, 500, 200 → 3 attempts, same `event_id` and body, each signature valid with a later `t`), `TestNotifier_GivesUp` | ✅ |
| AC-7 | `TestSign_KnownVector` (vector computed independently with Python `hmac`) | ✅ |
| AC-8 | `TestBanksim_Validation` (11 invalid bodies/keys → 400, nothing recorded; bad path key → 400), `TestConfig_Banksim`, `TestRun_MissingWebhookConfig_Exit1NamesVariable` | ✅ |
| AC-9 | Compose + curl (below) | ✅ |

Also:
- `TestBanksim_ShutdownStopsPendingWork`: cancel drops the pending webhook, and `Wait` returns.
- `TestBanksim_WithWalletAdapter`: the T013 adapter against this simulator, end to end (accept, idempotent retry, FAILED operation, `INVALID_ACCOUNT`, not found).

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 485 passed; `internal/banksim` 3× stable |
| Coverage `internal/banksim` | 96.9% |
| `make up` + `make e2e` | ✅ |
| curl `:9090/v1/debits` (IBAN …0000) | `202 {"bank_reference":"BSIM-01a1…","status":"PENDING"}`; the same request again → the identical 202 |
| curl `GET /v1/operations/{key}` | `PENDING` at once, `SUCCEEDED` after 3 s |
| curl payout IBAN …0002 | `422 INVALID_ACCOUNT`; `{}` body → `400 INVALID_REQUEST`; `/admin/scenarios` → `204` |
| Webhook in compose | Sent to `http://wallet:8080/webhooks/bank` and retried (attempts 1–4 logged with the same `event_id`). The wallet answers 404 until T015 adds the endpoint, which is expected. |

## Deviations from the plan
- `BANKSIM_WEBHOOK_DELAY` accepts 0 (settle at once); negative values are rejected.
- `GET /v1/operations/{key}` with a non-UUID key answers `400 INVALID_REQUEST` (the task did not say).
- Reusing a key on the other kind (a debit key for a payout) is `IDEMPOTENCY_KEY_REUSED`.
- Timing in tests uses short real delays (ms); the fake clock drives `occurred_at` and the signature `t`.

## Follow-ups
- DigitalOcean (done 2026-11-09 with doctl, on the user's request): the simulator runs as an internal-only service `banksim` in the app `shark-app` (same Dockerfile, build-time `APP=banksim`, `internal_ports: [9090]`, no ingress route, so `/admin/scenarios` is not public; one instance, as its state is in memory). The wallet's `BANK_BASE_URL` is `http://banksim:9090`; the simulator's `BANKSIM_WEBHOOK_URL` is `http://e-wallet:8080/webhooks/bank`; both components share a generated `BANK_WEBHOOK_SECRET` stored as an encrypted secret. Webhooks get 404 until T015 adds the endpoint.

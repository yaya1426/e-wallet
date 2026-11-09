# Implementation — T015 Bank webhook ingestion (inbox)

## Summary
New module `internal/webhook` (owns `inbox_events`):
- `POST /webhooks/bank` reads the raw body (≤ 64 KB), verifies `X-Bank-Signature` (HMAC-SHA256, ±5 min, constant-time, any of several `v1`), reads only `event_id` / `event_type`, stores the raw payload with `ON CONFLICT DO NOTHING`, wakes the workers and answers `200 {"status":"ok"}` (also for duplicates). Only a failed insert answers `503 TRY_AGAIN`.
- Inbox workers run on the shared queue engine (`queue="inbox"`). Per event: decode and validate the full payload into `BankEvent` (amount as `money.Money`); an undecodable payload or an unknown `event_type` → `FAILED` without calling the handler; otherwise `InTx(handler.HandleBankEvent)` → `DONE`, or `Retry` on any error (including `ErrUnknownTransaction`) → `DEAD` after `INBOX_MAX_ATTEMPTS`.
- `EventHandler` is set with `SetHandler` (payment, T016). `serve` starts the workers only when `INBOX_ENABLED` **and** a handler is set; until T016, events are stored and wait as `PENDING` (logged: "inbox processing disabled", reason "no bank event handler").

## Files
| Path | Change |
|------|--------|
| `migrations/000008_inbox.up/down.sql` | `inbox_events` (+ `UNIQUE (provider, provider_event_id)`, partial due index) |
| `internal/webhook/queries.sql`, `internal/sqlcgen` | Insert (rows affected tells stored vs duplicate), claim (SKIP LOCKED), stale-guarded marks; `sqlc.yaml` block |
| `internal/webhook/signature.go` | `Verify(secret, header, body, now)`, `ErrInvalidSignature`, `SignatureTolerance` |
| `internal/webhook/model.go` | `BankEvent`, event types/statuses, `EventHandler`, `ErrUnknownTransaction`, `Querier`, `Transactor`, `Observer` |
| `internal/webhook/store.go` | `queue.Store` over `inbox_events` (`MarkFailed` → `FAILED`) |
| `internal/webhook/service.go` | `NewService`, `SetHandler`, `HasHandler`, `Store`, `Run`, `process`, `decodeEvent` |
| `internal/webhook/http.go` | The endpoint, `CodeInvalidSignature` |
| `internal/platform/config` | `BANK_WEBHOOK_SECRET` (serve-only, ≥ 32 bytes), `INBOX_ENABLED=true`, `INBOX_WORKERS=4`, `INBOX_MAX_ATTEMPTS=10` (≥ 1) |
| `internal/platform/metrics` | `webhook_received_total{result}` |
| `cmd/wallet/app.go` | Builds the inbox and the handler, mounts the route (no user auth), starts/logs the workers |
| `api/openapi/wallet.yaml` (+ test) | `POST /webhooks/bank`, `BankEvent` schema, `INVALID_SIGNATURE` in the catalogue |
| `.golangci.yml` | depguard `webhook-deps` (only platform; T016 will allow `discrepancy`) |
| `configs/.env.example` | `INBOX_*` |
| T014 `implementation.md` | The DigitalOcean deployment note (simulator deployed as an internal service, 2026-11-09) |

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestWebhook_StoreAndProcess` (banksim-signed event → 200, one `PENDING` row with the raw payload; handler gets every field in a tx, its write commits, row `DONE`, `processed_at` set) | ✅ |
| AC-2 | `TestWebhook_DuplicateDelivery` (5 concurrent posts → 5 × 200, one row, handler once; a late duplicate after `DONE` → 200, not re-handled) | ✅ |
| AC-3 | `TestVerify` (19 cases incl. wrong secret, tampered body, missing header, ±6 min, several `v1`), `TestWebhook_BadRequests`, `TestWebhook_InvalidSignatureNotStored`, `TestVerify_ErrorHidesSecrets` | ✅ |
| AC-4 | `TestWebhook_BadRequests` (not JSON, array, null, missing `event_id`/`event_type`, oversized id → 400; > 64 KB → 413; nothing stored), `TestWebhook_DBDown_503` | ✅ |
| AC-5 | `TestInbox_UnknownTransactionThenDead` (retry waits for the backoff, 3 attempts, `DEAD`, `last_error`), `TestInbox_UnknownTypeFailed` (`FAILED`, handler not called), `TestDecodeEvent_*` | ✅ |
| AC-6 | `TestInbox_HandlerRollback` (handler writes then errors → write gone, event retried → `DONE` with one write) | ✅ |
| AC-7 | `TestVerify_KnownVector` (T014's vector), `TestVerify_SignedByBanksim` (`banksim.Sign` → `Verify`) | ✅ |
| AC-8 | `TestConfig_Inbox` (defaults, overrides, invalid values, secret length), `TestServe_InvalidConfig_Exit1NamesVariable` (short secret, bad `INBOX_WORKERS`), `TestServe_Inbox` (real serve: `INBOX_ENABLED=false` and no-handler both store and leave `PENDING`, with their log lines), `TestInbox_NotRunning_EventsWait` | ✅ |

Also: `TestService_RunNeedsAHandler`, `TestService_SetHandlerWiringBugs`, `TestNewService_InvalidConfig`, `TestWebhook_OnlyPost`.

## Verification
| # | Step | Command | Result |
|---|------|---------|--------|
| 1 | Tidy | `go mod tidy && git diff --exit-code go.mod go.sum` | ✅ no diff |
| 2 | Format | `gofmt -l .` | ✅ nothing |
| 3 | Vet | `go vet ./...` (also `-tags integration`) | ✅ |
| 4 | Lint | `golangci-lint run ./...` | ✅ 0 issues |
| 5 | Build + sqlc | `make ci` (tidy, fmt, vet, lint, sqlc-check, build, test, test-integration) | ✅ |
| 6–7 | Unit + integration (`-race`) | `go test -race -count=1 -tags=integration ./...` | ✅ 558 passed, 0 failed |
| 8 | Coverage | `go test -tags=integration -cover` | `internal/webhook` 98.2%, `config` 93.8%, `cmd/wallet` 84.6% |
| 9 | Run | `POSTGRES_HOST_PORT=5433 make up` | ✅ `/readyz` ok |
| 10 | HTTP checks | below | ✅ |
| 11 | Ledger integrity | not applicable: T015 writes no ledger entries (the cmd/wallet integration fixtures still run `ledgertest.AssertIntegrity`) | — |
| 12 | E2E + tear down | `make e2e && make down` | ✅ |

### HTTP checks (compose stack)
| Request | Expected | Actual |
|---------|----------|--------|
| banksim `POST /v1/debits` (…0000) and `/v1/payouts` (…0001) | simulator's webhooks stored | banksim "webhook delivered" on attempt 1; 2 rows `PENDING` (`debit.succeeded`, `payout.failed` + `ACCOUNT_CLOSED`) |
| `POST /webhooks/bank` signed with openssl | 200 `{"status":"ok"}` | 200 |
| same event twice more | 200, one row | 200, 200; `count = 1` |
| no header / wrong secret / tampered / t −6 min / t +6 min | 401 `INVALID_SIGNATURE` | 401 × 5 |
| `not json` / no `event_id` (signed) | 400 `MALFORMED_REQUEST` | 400 × 2 |
| 70 KB body | 413 `REQUEST_TOO_LARGE` | 413 |
| postgres stopped | 503 `TRY_AGAIN` | 503; after restart the same event → 200 |
| `GET /metrics` | `webhook_received_total` by result | stored 3, duplicate 2, invalid_signature 5, malformed 3 |
| wallet logs | no secret, no signature | 0 matches for the secret and for `v1=`; startup log "inbox processing disabled" reason "no bank event handler" |

## Deviations
None. Additions inside the task's intent:
- The endpoint is documented in `api/openapi/wallet.yaml`, which is the error-code catalogue (TDR-017 §3).
- An `event_id` longer than 200 characters is `MALFORMED_REQUEST` (bounds `provider_event_id`).
- The worker also rejects as `FAILED` a payload whose `status` does not match its `event_type`, or with a missing `bank_reference` / `idempotency_key` / `occurred_at`, a non-positive amount or a non-EGP currency: these can never be applied, so retrying would not help.

## Follow-ups
- T016: wire `payment` as the handler, the `UNKNOWN_BANK_EVENT` discrepancy on `DEAD`, and allow `discrepancy` in `webhook-deps`.
- There is no backlog gauge for the inbox (the task asked only for `webhook_received_total` and the queue metrics); an `inbox_pending_total` could be added with the alerting work.

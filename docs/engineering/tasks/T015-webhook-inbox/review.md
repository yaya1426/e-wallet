# Review — T015 (2026-11-09, round 1)
Verdict: Approved
Summary: The inbox follows TDR-007: the signature is checked on the raw body before parsing, the event is stored once with `ON CONFLICT DO NOTHING`, the endpoint answers 200 quickly, and the workers run each event's handler in its own transaction on the shared queue engine. Every AC has tests, and the live check confirmed real simulator webhooks are stored. One Minor and two Nits; none blocks.

| # | Severity | File:line | Rule (checklist item / TDR) | Finding | Suggested fix |
|---|----------|-----------|-----------------------------|---------|---------------|
| 1 | Minor | `internal/webhook/http.go:82` | TDR-007 §1 (5xx only when storing failed *temporarily*) | A signed body that is valid JSON but contains `\u0000` passes `json.Unmarshal`, yet PostgreSQL `jsonb` rejects it (`unsupported Unicode escape sequence`, SQLSTATE 22P05, verified on postgres:18). The endpoint answers 503 TRY_AGAIN, so the bank retries an event that can never be stored. The risk is low, because only the bank can sign events. | Map PgError 22P05 / 22P02 from the insert to `400 MALFORMED_REQUEST` (metric `malformed`), with a test. |
| 2 | Nit | `internal/webhook/signature.go:40` | C — simplicity / strict parsing | The duplicate-timestamp guard checks `ts != ""`, so `t=,t=123` is accepted. The signature still covers the timestamp that is used, so this is not exploitable. | Track "seen t" with a bool. |
| 3 | Nit | `internal/webhook/webhook_integration_test.go:230` | E — determinism | Two of the "nothing happens" checks rely on `time.Sleep(10 * tick)`. They can't fail falsely, but under load they may miss a real bug. | Acceptable as is. Alternatively, count claims with an observer and assert on that count. |

## Checked and fine
- **A Architecture:**
  - `webhook` imports only `platform` (`go list`: clock, httpserver, id, money, queue, its own sqlcgen).
  - depguard `webhook-deps` is added, and the other modules' rules already deny `webhook`.
  - It touches only `inbox_events`.
  - The handler has no SQL and no business rules.
  - `EventHandler` is defined by its consumer, as decided.
  - No HTTP types leave the module.
- **B Correctness:**
  - Amounts are `money.NewPositive` with a currency check; there are no floats.
  - The handler's writes and its error share one `InTx` (rollback test).
  - Marks happen after the commit, are stale-guarded, and processing is at-least-once.
  - Dedupe uses `UNIQUE (provider, provider_event_id)`.
  - No network call runs inside a transaction.
- **Security:**
  - HMAC-SHA256 compared with `hmac.Equal` over every `v1` value.
  - ±5 min window; the body is limited to 64 KB before the HMAC is computed.
  - Neither the secret nor the signature is logged or returned (unit test + live log grep). The access log has no headers.
- **C Go standards:**
  - Doc comments are present; naming has no stutter (`webhook.Handler`, `webhook.Service`).
  - Errors are wrapped with `%w`.
  - `Run` has a cancellation path; misuse (`Run` twice, or `SetHandler` after `Run`) is guarded.
  - `gofmt` / `vet` / `golangci-lint` report 0 issues.
- **D Data & API:**
  - The migration matches the task's DDL, with a partial due index and a down file.
  - The sqlc queries mirror the reviewed outbox queries.
  - Codes are `INVALID_SIGNATURE`, `MALFORMED_REQUEST`, `REQUEST_TOO_LARGE` and `TRY_AGAIN` per the task's table.
  - The OpenAPI spec documents the endpoint, and the catalogue test covers the new code.
- **E Tests:**
  - AC-1…AC-8 are mapped. Integration tests run against real Postgres, and duplicates are posted concurrently.
  - Backoff is driven by a fake clock.
  - `serve` was tested end to end for both "disabled" reasons.
  - Coverage: webhook 98.2%.
- **Plan conformance:**
  - Every file in the How table is done.
  - The additions (OpenAPI entry, `event_id` length bound, stricter payload validation → FAILED) are documented in implementation.md as within the task's intent.
  - There is no payment logic and no discrepancy (T016).

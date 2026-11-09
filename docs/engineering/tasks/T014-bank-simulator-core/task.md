# T014 — Bank simulator core

| Field        | Value |
|--------------|-------|
| ID           | T014 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T014-bank-simulator-core` |
| Depends on   | T013 (contract) |
| PRDs         | PRD-001 §9.1; PRD-002 §4; PRD-003 §4 |
| TDRs         | TDR-008 §2–4, TDR-007 §1 and §4, TDR-005 L3 |
| Docs changed | `engineering/roadmap.md` (settlement/statements → phase 2) |

## What
`cmd/banksim` becomes a real (in-memory) fake bank: it accepts debits and payouts idempotently,
answers status queries, and calls the wallet webhook asynchronously with an HMAC-signed event. It
supports the three phase-1 scenarios.

## Why
TDR-008 §2 (separate binary, in-memory state, HTTP only, idempotent, async signed webhook), §3
(scenarios by IBAN suffix + `/admin/scenarios`), TDR-007 §1 (signature format) and §4 (event types
and payload). Decided 2026-11-05: the scenario set, delay/retry defaults, and that settlement and
statements move to phase 2.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/banksim/server.go` | Routes, handlers |
| `internal/banksim/store.go` | Mutex-protected maps: operations by idempotency key, by bank reference; scenario overrides by IBAN |
| `internal/banksim/scenario.go` | Picks the scenario: one-shot override for the IBAN, else IBAN suffix |
| `internal/banksim/notifier.go` | Delayed, signed webhook delivery with retries (goroutines, stopped on shutdown) |
| `internal/banksim/signature.go` | `Sign(secret, t, body)` — header value `t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>` |
| `cmd/banksim/main.go` | Wire config, server, notifier |
| `internal/platform/config` | Banksim: `BANKSIM_WEBHOOK_URL` (required), `BANK_WEBHOOK_SECRET` (required, ≥ 32 bytes), `BANKSIM_WEBHOOK_DELAY=2s` |
| `docker-compose.yml`, `configs/.env.example` | `BANKSIM_WEBHOOK_URL=http://wallet:8080/webhooks/bank`, a dev secret shared with the wallet |
| `.golangci.yml` | depguard: `internal/banksim` must not import any wallet business module (it is an external system; it shares only the contract) |

### Data (migrations)
None (in memory; lost on restart — acceptable for a test double, TDR-008 §2).

### API
Implements `api/openapi/banksim.yaml` (T013) plus:

| Method | Path | Body | Response |
|--------|------|------|----------|
| POST | `/admin/scenarios` | `{ "iban": "EG…", "scenario": "0000" \| "0001" \| "0002" }` | `204`; applies to the **next** debit/payout for that IBAN only |
| GET | `/healthz` | — | unchanged |

Validation on `POST /v1/debits|payouts`: `Idempotency-Key` UUID, `amount_minor > 0`, `currency = EGP`,
non-empty `iban` and `holder_name`, `reference` = the key → otherwise `400 {error:{code:"INVALID_REQUEST"}}`.
Same key + different body → `422 IDEMPOTENCY_KEY_REUSED`. Same key + same body → the original response (202 or 422).

### Behaviour and design notes
- **Scenarios** (TDR-008 §3, phase-1 subset; every other suffix behaves like `0000`):
  | Scenario | Request answer | Later |
  |----------|----------------|-------|
  | `0000` | `202 {bank_reference, PENDING}` | after the delay: status `SUCCEEDED`, webhook `debit.succeeded` / `payout.succeeded` |
  | `0001` | `202` | after the delay: `FAILED`, webhook `debit.failed` (`INSUFFICIENT_FUNDS`, "Insufficient funds in the source account.") / `payout.failed` (`ACCOUNT_CLOSED`, "The destination account is closed.") |
  | `0002` | `422 {error:{code:"INVALID_ACCOUNT", message:"The bank account is not valid."}}` | nothing; no operation is recorded (`GET /v1/operations/{key}` → 404); repeating the key repeats the 422 |
- `bank_reference` = `BSIM-` + a new UUIDv7.
- **Webhook** (TDR-007 §4): `POST BANKSIM_WEBHOOK_URL` with
  `{event_id, event_type, bank_reference, idempotency_key, amount_minor, currency, status, failure_code, failure_message, occurred_at}`
  and `X-Bank-Signature`. `event_id` = new UUIDv7 per event (a retry of the same event reuses it).
  The status changes in the store **before** the first attempt, so `GET /v1/operations` shows the final status.
- **Delivery retries**: on a non-2xx answer or a network error, retry 5 times after 1, 2, 4, 8, 16 s
  (each retry is re-signed with a fresh `t`). After that it gives up and logs (the wallet's phase-2
  reconciliation covers this).
- The delay and retries run on goroutines with the root ctx; on SIGTERM they stop (pending webhooks
  are dropped, which is acceptable for the simulator). The clock is injectable for tests.
- It never imports the wallet's packages; it just speaks HTTP like a real bank.

### Out of scope
Scenarios `0003`–`0009`, `GET /v1/statements`, `GET /v1/accounts/settlement`, global random failure rate, JSON snapshot (all phase 2).

## Acceptance Criteria
- [x] AC-1 — Debit with IBAN `…0000` → `202` with a `BSIM-` reference; after the delay the webhook server (test `httptest`) gets one `debit.succeeded` with the right fields and a valid signature; `GET /v1/operations/{key}` → `SUCCEEDED`.
- [x] AC-2 — Payout `…0000` → `payout.succeeded`. Debit/payout `…0001` → `debit.failed`/`payout.failed` with the listed codes; operation `FAILED`.
- [x] AC-3 — `…0002` → `422 INVALID_ACCOUNT`, no webhook, operation `404`; the same key again → the same 422.
- [x] AC-4 — Same key + same body twice → the same `bank_reference`, only one webhook. Same key + different amount → `422 IDEMPOTENCY_KEY_REUSED`.
- [x] AC-5 — `/admin/scenarios` with `0001` for an IBAN ending `0000` → the next request fails, the one after succeeds again.
- [x] AC-6 — Webhook server returns 500 twice then 200 → delivered on the 3rd attempt with the same `event_id`; signatures valid each time.
- [x] AC-7 — The signature matches `HMAC-SHA256(secret, t + "." + raw_body)` (known-answer test vector recorded in the test).
- [x] AC-8 — Invalid request bodies → `400 INVALID_REQUEST`. Missing `BANKSIM_WEBHOOK_URL` / short secret → banksim exits 1 naming the variable.
- [x] AC-9 — With the compose stack up, `curl` to `:9090/v1/debits` works (checked in the implementation run).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1–AC-3 | unit (`httptest` both sides, fake clock) | `TestBanksim_Scenarios` (table) |
| AC-4 | unit | `TestBanksim_Idempotency` |
| AC-5 | unit | `TestBanksim_AdminOverrideOneShot` |
| AC-6 | unit | `TestNotifier_Retries` |
| AC-7 | unit | `TestSign_KnownVector` |
| AC-8 | unit | `TestBanksim_Validation`, `TestConfig_Banksim` |
| AC-9 | manual / e2e smoke | recorded in `implementation.md` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Body, delay, webhook retries, scenario set, admin override, settlement | defaults / no admin override yet | Answered | Defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Scenarios 0000/0001/0002 (+ one-shot `/admin/scenarios`); delay 2 s; webhook retries 1/2/4/8/16 s; `IDEMPOTENCY_KEY_REUSED`; settlement + statements → phase 2 | Yahya | roadmap, this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | Implementation started on top of T013 |
| 2026-11-09 | In Review | Implemented and verified; see implementation.md |
| 2026-11-09 | Approved | Review round 1: Approved (2 Nits) |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

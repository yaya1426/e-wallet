# T013 — Bank gateway port + banksim HTTP adapter

| Field        | Value |
|--------------|-------|
| ID           | T013 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T013-bank-gateway-port` |
| Depends on   | T002 |
| PRDs         | PRD-002 TU-BR-5, PRD-003 WD-BR-5, PRD-001 BR-19 |
| TDRs         | TDR-008 §1–2, TDR-005 L3, TDR-003 §5 |
| Docs changed | — (the banksim contract is written here as `api/openapi/banksim.yaml`) |

## What
The only way the wallet talks to a bank: the `bank.Gateway` interface, its HTTP adapter for the
simulator, a fake for unit tests, and the simulator's API contract (which T014 implements).

## Why
TDR-008 §1 (port, error classification, adapter, fake), TDR-005 L3 (`Idempotency-Key =
transaction_id`), TDR-003 §5 (minor units). Decided 2026-11-05: `BANK_HTTP_TIMEOUT=5s`, the
mapping below, `GetStatement` waits for phase 2.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/bank/gateway.go` | Interface + types + errors |
| `internal/bank/banksim/client.go` | HTTP adapter (`net/http`, no third-party client) |
| `internal/bank/banktest/fake.go` | In-memory `Gateway` with scripted responses per call and recorded calls |
| `api/openapi/banksim.yaml` | Simulator contract |
| `internal/platform/config` | `BANK_BASE_URL` (required by `serve` only, checked in `ValidateServe` like `JWT_SECRET`; absolute http(s) URL), `BANK_HTTP_TIMEOUT=5s` (> 0) |
| `configs/.env.example`, `docker-compose.yml` | `BANK_BASE_URL=http://banksim:9090` |
| `.golangci.yml` | depguard: `bank` imports only platform |

```text
type Kind string // DEBIT | PAYOUT
type DebitRequest  struct { IdempotencyKey uuid.UUID; Amount money.Money; IBAN, HolderName string }
type PayoutRequest struct { IdempotencyKey uuid.UUID; Amount money.Money; IBAN, HolderName string }
type Acceptance struct { BankReference string }
type OperationStatus string // PENDING | SUCCEEDED | FAILED
type Operation struct { IdempotencyKey uuid.UUID; BankReference string; Kind Kind; Status OperationStatus;
                        Amount money.Money; FailureCode, FailureMessage string }
type Gateway interface {
    CreateDebit(ctx, DebitRequest) (Acceptance, error)
    CreatePayout(ctx, PayoutRequest) (Acceptance, error)
    GetOperation(ctx, idempotencyKey uuid.UUID) (Operation, error)
}
type RejectedError struct { Code, Message string }   // errors.As target; business — do not retry
var ErrTemporary = errors.New("bank: temporary failure") // wrapped with details; retry
var ErrNotFound  = errors.New("bank: operation not found")
```

### Data (migrations)
None.

### API (banksim contract, `api/openapi/banksim.yaml`)
| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/v1/debits` | header `Idempotency-Key: <uuid>`; `{amount_minor, currency, iban, holder_name, reference}` (`reference` = the same UUID) | `202 {bank_reference, status:"PENDING"}`; `422 {error:{code,message}}` |
| POST | `/v1/payouts` | same | same |
| GET | `/v1/operations/{idempotency_key}` | — | `200 {idempotency_key, bank_reference, kind, status, amount_minor, currency, failure_code, failure_message}`; `404 {error:{code:"NOT_FOUND"}}` |

A repeated `Idempotency-Key` with the same body returns the original response.

### Behaviour and design notes
- Every call uses `http.NewRequestWithContext` + a client with `Timeout = BANK_HTTP_TIMEOUT`.
  Response bodies are size-limited (64 KB) and always closed.
- **Error mapping** (the caller never sees HTTP):
  | Bank answer | Result |
  |-------------|--------|
  | `202` with a non-empty `bank_reference` | `Acceptance` |
  | `200` (operations) | `Operation` |
  | `404` on `GetOperation` | `ErrNotFound` |
  | `408`, `429` | `ErrTemporary` |
  | other `4xx` with `error.code` | `*RejectedError{Code, Message}` |
  | other `4xx` without a parsable code | `*RejectedError{Code:"BANK_REJECTED", Message: status text}` |
  | `5xx`, timeout, connection error, unparsable 2xx body | `ErrTemporary` (wrapped) |
- Amounts go over the wire as `amount_minor` int64 and `currency` (TDR-003 §5). An `Operation` with a
  currency other than EGP → `ErrTemporary` (logged; it should never happen).
- Logs: `bank_op`, `idempotency_key`, `status`, `duration`. Metric `bank_call_duration_seconds{op,result}` (TDR-006 §6).
- The fake records calls and can be scripted per idempotency key (e.g. "time out twice then accept").

### Out of scope
`GetStatement` / settlement balance (phase 2), the simulator itself (T014), callers (T017/T018).

## Acceptance Criteria
- [x] AC-1 — Against an `httptest` server: `CreateDebit` sends `POST /v1/debits` with `Idempotency-Key` = the UUID, a JSON body with `amount_minor`/`currency`/`iban`/`holder_name`/`reference`, and returns the `bank_reference` from a 202. Same for `CreatePayout`.
- [x] AC-2 — Each row of the mapping table produces the listed result (`errors.As` for `*RejectedError`, `errors.Is` for `ErrTemporary`/`ErrNotFound`).
- [x] AC-3 — A server slower than `BANK_HTTP_TIMEOUT` → `ErrTemporary` within about the timeout.
- [x] AC-4 — `GetOperation` parses all fields into `Operation` (amount as `money.Money`).
- [x] AC-5 — `bank_call_duration_seconds` is observed with `op` and `result` labels.
- [x] AC-6 — Config: `BANK_BASE_URL` missing/invalid or `BANK_HTTP_TIMEOUT` ≤ 0 → error naming the variable.
- [x] AC-7 — `api/openapi/banksim.yaml` passes the same OpenAPI lint as `wallet.yaml`.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-4 | unit (`httptest`) | `TestClient_CreateDebit`, `TestClient_CreatePayout`, `TestClient_GetOperation` |
| AC-2 | unit | `TestClient_ErrorMapping` (table) |
| AC-3 | unit | `TestClient_Timeout` |
| AC-5 | unit | `TestClient_Metrics` |
| AC-6 | unit | `TestConfig_Bank` |
| AC-7 | CI | OpenAPI lint |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Timeout and error mapping | defaults / all 4xx temporary | Answered | Defaults |
| 2 | Where is `BANK_BASE_URL` required? (DigitalOcean sets neither it nor a simulator yet) | serve only, user sets it on DO / serve only, set via doctl / every command | Answered | Serve only (`ValidateServe`). `BANK_BASE_URL=http://banksim:9090` was added to the DO e-wallet service with doctl on 2026-11-09 (user-approved; a placeholder until T014 deploys the simulator) |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-09 | `BANK_BASE_URL` is checked by `serve` only, so the migrate pre-deploy job runs without it; set on DigitalOcean via doctl (user-approved) | Yahya | this task |
| 2026-11-05 | `BANK_HTTP_TIMEOUT=5s`; 4xx → rejected except 408/429; 5xx/timeout → temporary; 404 op → not found; `GetStatement` phase 2; fake in `bank/banktest` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | Open question 2 answered (BANK_BASE_URL serve-only); implementation started on top of T012 |
| 2026-11-09 | In Review | Implemented and verified; see implementation.md |
| 2026-11-09 | Approved | Review round 1: Approved (1 Nit) |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

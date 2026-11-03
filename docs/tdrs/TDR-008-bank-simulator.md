# TDR-008 — External Bank: Gateway Port + Bank Simulator

| Field      | Value |
|------------|-------|
| Status     | Proposed |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-002, PRD-003, TDR-006, TDR-007, TDR-009 |

## Context
There is no real bank API yet. We still need to build and test real async behaviour: delays,
failures, timeouts, duplicate and lost webhooks, and status queries used by reconciliation.

## Decision

### 1. Port (interface) inside the wallet
`internal/bank` defines the only way the wallet talks to a bank:
```text
type Gateway interface {
    CreateDebit(ctx, DebitRequest)   (Acceptance, error)  // top-up: pull from user's bank account
    CreatePayout(ctx, PayoutRequest) (Acceptance, error)  // withdrawal: push to user's bank account
    GetOperation(ctx, idempotencyKey string) (Operation, error)   // reconciliation
    GetStatement(ctx, date civil.Date) (Statement, error)         // daily reconciliation
}
```
Errors are classified by the adapter into `ErrRejected{Code, Message}` (business — do not retry)
vs `ErrTemporary` (timeout, 5xx, network — retry). Callers never see HTTP details.

Adapter `internal/bank/banksim` implements it over HTTP. A real bank later = a new adapter,
nothing else changes.

### 2. Bank simulator = separate binary in the same repo
`cmd/banksim` — a small Go HTTP server that **acts like an external bank**. It runs as its own
process (own container in docker-compose), keeps its state in memory (a mutex-protected map,
with an optional JSON snapshot file; it is a test double, so durability is not needed),
and talks to the wallet **only over HTTP** like a real bank would.

API (minor units everywhere):
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/debits` | Accept a debit; header `Idempotency-Key`; returns `202 {bank_reference, status: PENDING}` |
| POST | `/v1/payouts` | Accept a payout; same contract |
| GET | `/v1/operations/{idempotency_key}` | Current status of an operation |
| GET | `/v1/statements/{yyyy-mm-dd}` | Settlement-account movements for a day |
| GET | `/v1/accounts/settlement` | Company settlement balance |
| POST | `/admin/scenarios` | (test only) set the scenario for the next requests |

Repeated `Idempotency-Key` → original response, no new operation.
After a configurable delay it **calls the wallet webhook** with an HMAC-signed event (TDR-007).

### 3. Scenarios (deterministic, chosen by the user's test IBAN suffix, overridable via `/admin/scenarios`)
| IBAN ends with | Behaviour |
|----------------|-----------|
| `…0000` | Success after 2 s, webhook sent once |
| `…0001` | Business rejection: `INSUFFICIENT_FUNDS` (debit) / `ACCOUNT_CLOSED` (payout), via webhook |
| `…0002` | Synchronous `422` rejection at request time (`INVALID_ACCOUNT`) |
| `…0003` | Success, **webhook never sent** (tests reconciliation) |
| `…0004` | Success, **webhook sent 3 times** (tests dedupe) |
| `…0005` | First 3 requests **time out** (sleep > client timeout) and are still recorded, then success (tests retry + idempotency) |
| `…0006` | Returns `503` for 5 minutes, then normal (tests backoff) |
| `…0007` | Long delay: webhook after 15 min (tests reconciliation finding a still-pending op) |
| `…0008` | Webhook with **wrong amount** (tests discrepancy) |
| `…0009` | Webhook with **invalid signature** first, valid one 30 s later |

Global knobs (env): base delay, random failure rate, webhook URL, HMAC secret.

### 4. Where it is used
- Local dev (`docker compose up` starts wallet + postgres + banksim).
- Integration / end-to-end tests (`test/e2e`), each scenario above is a test case.
- Unit tests use an in-process fake implementing `bank.Gateway` (no HTTP).

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| Mock inside the wallet process | Would not exercise HTTP, timeouts, signatures or real async webhooks. |
| WireMock / Mockoon | Hard to model stateful idempotency, delayed callbacks and statements. |
| A bank sandbox | Not available yet. |

## Consequences
- + Realistic failure testing from day one; swapping to a real bank is an adapter change.
- − We maintain a second small binary. It is test tooling and must never be deployed to production.

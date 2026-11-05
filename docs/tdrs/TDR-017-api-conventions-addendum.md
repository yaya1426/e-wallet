# TDR-017 — API Conventions Addendum: 413, 429 and Idempotency Lock Waits

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 task planning) |
| Date       | 2026-11-05 |
| Deciders   | Yahya Elaraby |
| Related    | Extends TDR-013 (status codes); clarifies TDR-005 L1 + TDR-010 §5; T004, T007 |

## Context
TDR-013 lists the HTTP status codes of the API. While planning T004 two more were needed: a body
larger than the 64 KB limit, and the login rate limit (TDR-011). While planning T007, TDR-005 L1
("lock wait on the same key → 409 `REQUEST_IN_PROGRESS`") and TDR-010 §5 ("lock timeout → retry
the whole tx, then 503 `TRY_AGAIN`") gave two different answers for the same SQLSTATE `55P03`.

## Decision
1. Add to the TDR-013 status list:
   - `413` — request body larger than 64 KB, code `REQUEST_TOO_LARGE`;
   - `429` — rate limited, code `TOO_MANY_REQUESTS`.
2. A lock timeout while inserting the **idempotency key** is not a contention to retry: the other
   request with the same key is still running. `idempotency.Begin` turns it into the plain sentinel
   `ErrRequestInProgress` (it does not wrap the `*pgconn.PgError`), so `database.InTx` does not
   retry it, and the API answers `409 REQUEST_IN_PROGRESS`. Every other lock timeout follows TDR-010 §5.
3. The catalogue of error codes is the OpenAPI file `api/openapi/wallet.yaml`. Each endpoint task
   adds its codes there.

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| Use 400 for a too-large body | One code fewer | Not standard HTTP | 413 is what clients expect |
| Let InTx retry the key lock, answer 503 | No special case | Contradicts TDR-005; the client sees "try again" instead of "in progress" | TDR-005 wins |

## Consequences
- Positive: one clear answer per case.
- Negative / trade-offs: `idempotency.Begin` must map one SQLSTATE itself.

## Revisit When
Idempotency keys move to a two-transaction design (an `IN_PROGRESS` row committed early, TDR-005 L1 note).

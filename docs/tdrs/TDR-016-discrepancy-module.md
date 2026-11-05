# TDR-016 — `discrepancy` Module Owns Reconciliation Discrepancies

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning) |
| Date       | 2026-11-05 |
| Deciders   | Yahya Elaraby |
| Related    | Extends the module list of TDR-001 §2 / TDR-012 §1; TDR-007 §3; TDR-009 §3; architecture §3 |

## Context
TDR-007 has `payment.ApplyBankOutcome` record a discrepancy (terminal transaction with a
different bank outcome, amount mismatch), and the webhook inbox records `UNKNOWN_BANK_EVENT`.
The table `reconciliation_discrepancies` was owned by `reconciliation`, but the allowed import
direction is `reconciliation → payment` (architecture §3.2). `payment` and `webhook` therefore
could not record a discrepancy without an import cycle, and the top-up/withdrawal tasks
(phase 1) would have to wait for the reconciliation module (phase 2).

## Decision
- A new business module **`internal/discrepancy`** owns `reconciliation_discrepancies`
  (columns as in TDR-009 §3).
- It exposes `Record(ctx, tx pgx.Tx, Discrepancy) error` (runs inside the caller's DB
  transaction, so the discrepancy commits atomically with the rest) and `List(ctx, filter)`
  for the operator API.
- It depends on nothing but `platform`. `payment`, `webhook`, `reconciliation` and the HTTP API
  may import it.
- It never changes money or transaction status.
- It is built in phase 1 together with `payment.ApplyBankOutcome` (task T016).

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| Interface in `payment`, implemented by `reconciliation`, wired in `main` | Ownership unchanged | `reconciliation` must exist in phase 1 | Delays nothing less, more wiring |
| Caller of `ApplyBankOutcome` records it | No new module | Logic duplicated in webhook and recon; harder to keep one DB tx | Error-prone |

## Consequences
- Positive: no import cycle; discrepancies can be recorded from phase 1.
- Negative / trade-offs: one more (small) module.
- Follow-ups: add `internal/discrepancy` to the `depguard` platform deny list when the module is created (T016).

## Revisit When
Discrepancy handling grows its own workflow (assignment, resolution flow) — it may then merge
with an operator/case-management module.

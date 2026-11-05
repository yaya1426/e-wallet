# TDR-003 — Money Representation: int64 Piasters

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-05, phase-1 roadmap planning) |
| Date       | 2026-11-03 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-001 BR-1..BR-4 |

## Context
Floating point cannot represent most decimal fractions exactly (0.1 + 0.2 ≠ 0.3). Any float in a
money path causes rounding drift and reconciliation breaks. EGP has a minor unit:
1 EGP = 100 piasters (قرش).

## Decision
1. **Every amount is an integer number of piasters** (minor units, ISO 4217 exponent 2 for EGP).
2. **Database:** `BIGINT NOT NULL` columns named `amount` / `balance` (unit = piasters) with
   `CHECK (amount > 0)` where applicable, plus a `currency CHAR(3) NOT NULL DEFAULT 'EGP'`
   column with `CHECK (currency = 'EGP')` — kept so multi-currency can be added later without
   a breaking migration.
3. **Go:** a small value type in `internal/platform/money`:
   ```text
   type Money struct { Amount int64; Currency Currency }   // Amount in minor units
   ```
   (Decided 2026-11-05, T002: the fields are **unexported** with `Amount()` / `Currency()`
   accessors, so an invalid value can only come from the constructors.)
   - Constructors validate currency and sign (decided 2026-11-05): `New(amount, cur)` accepts
     any sign (balances; system accounts may be negative, TDR-010 §3); `NewPositive(amount, cur)`
     rejects amounts ≤ 0 and is used for every movement amount (PRD-001 BR-4).
   - `Add`/`Sub` check currency equality and **overflow** (return error, never wrap).
   - No `float64` anywhere in money code; a lint rule (`forbidigo`) bans `float32/float64` in
     money, ledger, wallet, payment packages.
   - Formatting for display (`150.75 EGP`) is done with integer division/modulo only.
4. **API:** amounts are JSON integers in `amount_minor` plus `currency`. Decimal strings
   (`"150.75"`) and floats are **rejected** (strict JSON decoding into `int64`).
5. **Bank gateway:** the simulator speaks minor units too. If a real bank later requires
   decimal strings, conversion is done only in the bank adapter, from integers, without floats.
6. No division of money exists in this phase (no fees, no splitting). If needed later, a
   documented rounding rule (banker's rounding, remainder to a named account) will be a new TDR.

## Alternatives Considered
| Option | Why not chosen |
|--------|----------------|
| `float64` | Inexact. Forbidden. |
| `NUMERIC(19,2)` + `shopspring/decimal` | Correct but slower, easy to mix scales, and integers already cover EGP exactly. |
| Postgres `MONEY` type | Locale-dependent; poorly supported by drivers. |

## Consequences
- + Exact arithmetic; trivial sums in SQL; fast.
- + int64 max ≈ 9.2 × 10^16 EGP — no practical overflow risk, still checked.
- − Clients must convert for display (documented in the API spec).

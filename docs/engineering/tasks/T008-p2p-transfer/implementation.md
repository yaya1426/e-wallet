# T008 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T008-p2p-transfer`, stacked on T007 (PR #9, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **`internal/transfer`**:
  - `Service.Send` runs the whole transfer in one `InTx`: `idempotency.Begin` → checks 8–14 → `transaction.Create` (P2P, COMPLETED) → `ledger.Post` (Dr sender / Cr recipient) → `idempotency.Complete`.
  - Business errors (checks 8–14) are rendered, stored with `Complete` and committed, so they are replayed. Every other error rolls back.
  - The handler does checks 1–7 before the unit of work, hashes the **normalized** request (trimmed note), and maps errors (idempotency → 400/409/422, `ErrTryAgain` → 503, otherwise 500).
- **`internal/wallet/limits.go`**: `Limits` + `CheckP2PAmount/TopUpAmount/WithdrawalAmount`, `CheckDailyOutgoing`, `CheckRecipientBalance`, `CheckTopUpBalance`, written as `amount > limit − current` so they cannot overflow. Also `wallet.ErrFrozen` and `wallet.ErrInsufficientFunds`, and the shared money error codes.
- **`internal/identity`**: `MaskName`, `ValidPhone`. `GetUserByPhone` now takes a `Querier` (see Deviations).
- **`internal/platform/httpserver`**: `ErrorBody` (the TDR-013 body, so idempotency can store it) and `WriteTryAgain` (`ErrTryAgain` → 503 `TRY_AGAIN`).
- **Config**: `LIMIT_*` (8 variables), each > 0 and each min ≤ max; mapped to `wallet.Limits` in `cmd/wallet/app.go`.
- **OpenAPI**: `POST /v1/transfers` with the full check order, the `Idempotency-Key` parameter, the `Idempotent-Replayed` header, and every code in the catalogue test.
- **depguard**: `transfer-deps`.
- **Test fixture**: `newAPIFixtureWith(clock, env)` loads the real config defaults (limits included); `fund` credits a wallet through a COMPLETED top-up + `TOPUP_SETTLE` entry.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestTransfer_Success` (201 body, balances 15 000 / 5 000, 1 `P2P` entry with 2 postings, 1 transaction shared by both, note trimmed, absent note → `null`) | ✅ |
| AC-2 | `TestTransfer_Errors` (one sub-test per row 1–14; transactions/entries count and sender balance unchanged after all of them) | ✅ |
| AC-3 | `TestTransfer_RecipientFrozenStillReceives` (frozen recipient receives; frozen sender → 403) | ✅ |
| AC-4 | `TestTransfer_IdempotentReplay`, `TestTransfer_KeyReused`, `TestTransfer_BusinessErrorReplayed` (stored `INSUFFICIENT_FUNDS` replayed after funding) | ✅ |
| AC-5 | `TestTransfer_ParallelDrainNeverNegative` (20 parallel → 10 × 201, 10 × `INSUFFICIENT_FUNDS`, final 0) | ✅ |
| AC-6 | `TestTransfer_OppositeDirectionsNoDeadlock` (50 + 50 concurrent, all 201, balances unchanged) | ✅ |
| AC-7 | `TestTransfer_DailyLimit_CairoDay` (fake clock: yesterday's 9 000 at 21:59Z excluded; today's P2P + a PENDING withdrawal counted; exactly the limit allowed) | ✅ |
| AC-8 | `TestMaskName` (incl. Arabic, extra spaces, tab) | ✅ |
| AC-9 | config table (≤ 0, non-number, decimal, min > max) + CLI `serve` exits 1 naming `LIMIT_P2P_MIN` | ✅ |
| AC-10 | `ledgertest.AssertIntegrity` in every API fixture cleanup; live SQL check | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, fmt, vet, lint, sqlc-check, build, unit, integration) | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 352 passed, 0 failed |
| Coverage `internal/transfer` (`-coverpkg`, HTTP tests) | 86.0% |
| `make up` + `make e2e` | ✅ |
| Live HTTP (wallet funded with one balanced SQL top-up) | transfer 201 (`masked_name` "Mona H.", `balance_after_minor` 15000); same key → replay + `Idempotent-Replayed: true`; same key, other amount → 422 `IDEMPOTENCY_KEY_REUSED`; no key → 400; too much → 422 `INSUFFICIENT_FUNDS`; self → 422; recipient balance 5000; ledger debits = credits = 25000, 0 drift |

## Review fixes (round 1 → 2, 2026-11-09)
- Tests for `WriteTryAgain` / `ErrorBody` (the 503 row).
- One `businessErrors` table for storing and rendering; unknown errors → 500.
- The API fixture counts `InTx` retries; the opposite-direction test proves 0 deadlocks.
- Validation-order, note-length and "earlier check wins" tests; a FAILED withdrawal is excluded from the daily limit.
- OpenAPI documents replay behaviour.
- **Re-verification:** `make ci` exit 0, 359 tests pass.

## Deviations from the plan
- **The sender's frozen status is read before `LockAccounts`, without a lock** (task step order). A freeze committed during a transfer doesn't stop that transfer. Freezing is a manual SQL step in phase 1 (PRD-001 BR-8), so this is accepted rather than adding a second row lock per transfer (review #3).
- **`identity.GetUserByPhone(ctx, q, phone)` takes a `Querier`.** The transfer reads the recipient inside its unit of work. With the old pool-only read, every concurrent transfer would hold one connection and wait for a second, which can exhaust the pool under the parallel tests. Callers were updated.
- **`Service.Send` takes a `Renderer`:** the handler renders the response, and the service stores it inside the unit of work. The HTTP shapes stay in `http.go`, and the stored body is exactly what is sent.
- **Shared money codes live in `wallet`** (`INSUFFICIENT_FUNDS`, `WALLET_FROZEN`, the limit codes, `INVALID_AMOUNT`, `UNSUPPORTED_CURRENCY`) so T017/T018 reuse them; P2P-only codes are in `transfer`.
- **Replays are equal JSON, not equal bytes** (`jsonb`, see T007).
- **depguard `transfer-deps` is not probed:** every module it forbids (outbox, bank, payment, …) is still unwritten.

## Follow-ups (not done)
- None.

# T008 — P2P transfer

| Field        | Value |
|--------------|-------|
| ID           | T008 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T008-p2p-transfer` |
| Depends on   | T006, T007 |
| PRDs         | PRD-004 (all), PRD-001 BR-4, BR-6, BR-8, §5.6 |
| TDRs         | TDR-003, TDR-004 §3, TDR-005, TDR-010, TDR-013 |
| Docs changed | — |

## What
`POST /v1/transfers`: send money to another user by phone number, synchronously, in one DB
transaction. Also the limits configuration shared by all flows.

Deliverables:
- `internal/transfer` orchestrator: `Send`.
- `internal/wallet/limits.go`: `Limits` config + pure checks.
- `identity.MaskName` (also used by T009/T010).
- `httpserver` mapping of `database.ErrTryAgain` → `503 TRY_AGAIN`.
- Config `LIMIT_*` (all of PRD-001 §5.6).
- OpenAPI additions.

## Why
PRD-004 P2P-1…4, §3 checks 1–8, P2P-BR-1…5; TDR-010 (ordered locks, retries, 503); TDR-004 §3
(Dr sender / Cr recipient, entry type `P2P`); TDR-005 L1/L2; PRD-001 §5.6 (limits, Cairo day).

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `internal/transfer/service.go`, `model.go`, `http.go` | Use case + handler |
| `internal/wallet/limits.go` | `Limits{TopUpMin, TopUpMax, WithdrawalMin, WithdrawalMax, P2PMin, P2PMax, MaxBalance, DailyOutgoing}` (int64 piasters) + `CheckP2PAmount`, `CheckDailyOutgoing(spentToday, amount)`, `CheckRecipientBalance(balance, amount)` (and the top-up/withdrawal checks used later) |
| `internal/wallet/service.go` | Uses `GetByUserID(ctx, q, userID)` (from T004: wallet row incl. `ledger_account_id`, status). Locking goes through `ledger.LockAccounts`, not the wallet row |
| `internal/identity/mask.go` | `MaskName("Ahmed Mohamed Ali") = "Ahmed M. A."` — first word in full, the first rune of every other word + `.`; words split on Unicode spaces |
| `internal/platform/config` | `LIMIT_TOPUP_MIN=1000`, `LIMIT_TOPUP_MAX=3000000`, `LIMIT_WITHDRAWAL_MIN=1000`, `LIMIT_WITHDRAWAL_MAX=3000000`, `LIMIT_P2P_MIN=100`, `LIMIT_P2P_MAX=3000000`, `LIMIT_MAX_BALANCE=10000000`, `LIMIT_DAILY_OUTGOING=6000000` (all piasters, > 0, each min ≤ max) |
| `internal/platform/httpserver/errors.go` | A helper that writes `503 TRY_AGAIN` for `errors.Is(err, database.ErrTryAgain)` |
| `.golangci.yml` | depguard: `transfer` may import `wallet`, `transaction`, `ledger`, `identity`, `idempotency` + platform |
| `api/openapi/wallet.yaml` | Endpoint |

### Data (migrations)
None.

### API
**`POST /v1/transfers`** — headers `Authorization`, `Idempotency-Key`.
```json
{ "recipient_phone": "+201001234567", "amount_minor": 5000, "currency": "EGP", "note": "lunch" }
```
`201 Created`
```json
{ "transaction_id": "…", "type": "P2P", "status": "COMPLETED", "amount_minor": 5000, "currency": "EGP",
  "recipient": { "phone": "+201001234567", "masked_name": "Ahmed M. A." },
  "note": "lunch", "balance_after_minor": 15000, "created_at": "2026-11-05T12:00:00Z" }
```
`note` is `null` when absent or empty after trimming.

Errors, in **check order**:
| # | Check | HTTP | Code |
|---|-------|------|------|
| 1 | Token | 401 | `UNAUTHENTICATED` |
| 2 | Body decodes (unknown fields, `amount_minor` not an integer → decode error) | 400 | `MALFORMED_REQUEST` |
| 3 | `Idempotency-Key` | 400 | `IDEMPOTENCY_KEY_REQUIRED` / `IDEMPOTENCY_KEY_INVALID` |
| 4 | `amount_minor` ≤ 0 | 400 | `INVALID_AMOUNT` |
| 5 | `currency` ≠ `EGP` | 400 | `UNSUPPORTED_CURRENCY` |
| 6 | Phone format (T004 rule) | 400 | `INVALID_PHONE` |
| 7 | Note > 140 runes after trim | 400 | `INVALID_NOTE` |
| — | *idempotency `Begin` (replay / 422 `IDEMPOTENCY_KEY_REUSED` / 409 `REQUEST_IN_PROGRESS`)* | | |
| 8 | Amount outside `[P2P_MIN, P2P_MAX]` | 422 | `AMOUNT_OUT_OF_RANGE` |
| 9 | Recipient phone not registered | 404 | `RECIPIENT_NOT_FOUND` |
| 10 | Recipient = sender | 422 | `SELF_TRANSFER_NOT_ALLOWED` |
| 11 | Sender wallet `FROZEN` | 403 | `WALLET_FROZEN` |
| 12 | Sender balance < amount (under lock) | 422 | `INSUFFICIENT_FUNDS` |
| 13 | Today's outgoing + amount > `DAILY_OUTGOING` | 422 | `DAILY_LIMIT_EXCEEDED` |
| 14 | Recipient balance + amount > `MAX_BALANCE` | 422 | `RECIPIENT_LIMIT_EXCEEDED` |
| — | DB conflict after retries | 503 | `TRY_AGAIN` |

Checks 8–14 are business errors: stored by idempotency `Complete` and replayed for the same key.

### Behaviour and design notes
One `InTx` (TDR-010; `fn` has no side effects outside the tx):
1. `idempotency.Begin` → replay if any.
2. Range check (8).
3. `identity.GetUserByPhone` (9), compare IDs (10). Load both wallets with `wallet.GetByUserID`.
4. Sender wallet `FROZEN` → (11). The recipient may be `FROZEN` (PRD-001 BR-8).
5. `ledger.LockAccounts([sender, recipient])` — ordered by account ID inside `LockAccounts`.
6. Balance (12) from the locked rows; `transaction.SumOutgoingForDay(sender wallet, clock.CairoDay(now))` (13);
   recipient balance (14).
7. `transaction.Create` (P2P, `COMPLETED`, source/destination wallets, note, idempotency key).
8. `ledger.Post(Entry{TransactionID, Type: P2P, Postings: Dr sender / Cr recipient})`.
9. Build the response (`balance_after_minor` = sender's balance after) → `idempotency.Complete(201, body, &txnID)`.
- On a business error at steps 2–6: `idempotency.Complete(status, errorBody, nil)` and commit (no
  transaction row, nothing posted), then return that error response.
- Note handling: trim spaces, empty → null, ≤ 140 runes.
- Masked name comes from `identity.MaskName(recipient.FullName)`.

### Out of scope
Reversal / cancel (P2P-BR-3), recipient preview (T009), history (T010), notifications.

## Acceptance Criteria
- [x] AC-1 — Given A (balance 20 000) sends 5 000 to B, then `201` with the body above; A = 15 000, B += 5 000; one `P2P` journal entry with 2 postings; one transaction `P2P COMPLETED` shared by both.
- [x] AC-2 — Each row of the error table returns its status + code; nothing is posted and no transaction row is created for any of them.
- [x] AC-3 — Recipient wallet `FROZEN` → transfer succeeds. Sender `FROZEN` → `403 WALLET_FROZEN`.
- [x] AC-4 — Same key + same body twice → second response equals the first + `Idempotent-Replayed: true`, one transfer. Same key + different amount → `422 IDEMPOTENCY_KEY_REUSED`. A stored `INSUFFICIENT_FUNDS` is replayed even after the sender has been funded.
- [x] AC-5 — 20 parallel transfers of 1 000 from a wallet with 10 000 (different keys) → exactly 10 succeed, 10 get `INSUFFICIENT_FUNDS`, final balance 0, never negative (PRD-004 AC-3).
- [x] AC-6 — A→B and B→A, 50 each, concurrently, with the default DB settings → every request succeeds (no 5xx and no `503`; ordered locks prevent deadlocks), and the final balances are correct (PRD-004 AC-2).
- [x] AC-7 — The daily limit counts today's (Cairo) completed P2P + withdrawals PENDING/PROCESSING/COMPLETED; yesterday's do not count (fake clock across 22:00Z).
- [x] AC-8 — `MaskName`: `"Ahmed Mohamed Ali"→"Ahmed M. A."`, `"Mona"→"Mona"`, `"  sara   ali "→"sara a."`, Arabic `"أحمد محمد"→"أحمد م."`.
- [x] AC-9 — Invalid limit config (≤ 0, min > max, non-number) → `wallet serve` exits 1 naming the variable.
- [x] AC-10 — `ledgertest.AssertIntegrity` passes after every scenario (PRD-004 AC-5).

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-3 | integration HTTP | `TestTransfer_Success`, `TestTransfer_RecipientFrozenStillReceives` |
| AC-2 | integration HTTP | `TestTransfer_Errors` (table, one sub-test per row) |
| AC-4 | integration HTTP | `TestTransfer_IdempotentReplay`, `TestTransfer_KeyReused`, `TestTransfer_BusinessErrorReplayed` |
| AC-5 | integration | `TestTransfer_ParallelDrainNeverNegative` |
| AC-6 | integration | `TestTransfer_OppositeDirectionsNoDeadlock` |
| AC-7 | integration | `TestTransfer_DailyLimit_CairoDay` |
| AC-8 | unit | `TestMaskName` |
| AC-9 | unit | `TestConfig_Limits` |
| AC-10 | integration | integrity check in every test cleanup |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Response body, masking format, note rules | defaults / stronger masking | Answered | Defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Masked name "Ahmed M. A."; 201 body as above; note trim/empty→null/≤140 runes → `INVALID_NOTE`; checks in PRD-004 §3 order; `LIMIT_*` env names | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | implement-task started, stacked on T007 |
| 2026-11-09 | In Review | Implemented and verified: make ci ok, 352 tests, live HTTP + integrity ok |
| 2026-11-09 | Changes Requested | Review round 1: 1 Major (503 row untested), 4 Minor, 1 Nit |
| 2026-11-09 | Approved | Review round 2: fixed (#3 accepted as deviation); make ci ok, 359 tests |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

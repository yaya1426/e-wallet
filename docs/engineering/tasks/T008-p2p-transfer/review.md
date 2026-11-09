# Review — T008

## Round 2 (2026-11-09)

Verdict: **Approved**

Summary: The Major and Minors #2, #4, #5 are fixed and tested; #3 is accepted as a documented
deviation; Nit #6 is documented. `make ci` passes (exit 0, lint 0 issues), 359 tests pass, and
`transfer` coverage from the HTTP tests is 84.3%.

| # | Round-1 severity | Status | Evidence |
|---|------------------|--------|----------|
| 1 | Major | **Fixed** | `TestWriteTryAgain` (wrapped `ErrTryAgain` + PgError → 503 `TRY_AGAIN` with `request_id`; another error → false); `TestErrorBody_EqualsWriteError` |
| 2 | Minor | **Fixed** | One `businessErrors` table drives `isBusinessError` and `render`; an unknown error renders 500; `writeValidationError` has explicit cases, default 500. Tests: `TestRender_BusinessErrorTable`, `TestWriteValidationError_Unknown` |
| 3 | Minor | **Accepted** | Recorded in `implementation.md` deviations (task step order; freezing is a manual SQL step in phase 1) |
| 4 | Minor | **Fixed** | The API fixture counts `InTx` retries (`txRetries`); `TestTransfer_OppositeDirectionsNoDeadlock` runs 5 goroutines per direction and asserts **0 deadlock retries** |
| 5 | Minor | **Fixed** | `TestValidate_Order` (4 → 5 → 6 → 7), `TestNormalizeNote` (140/141 Arabic), `TestTransfer_EarlierCheckWins` (12 before 13, 8 before 9), AC-7 now also has a FAILED withdrawal that does not count |
| 6 | Nit | **Fixed** | OpenAPI description: replays (201 and 4xx) carry `Idempotent-Replayed: true` and the first response's body, including its `request_id` |

New code reviewed against the checklist: no new findings.

## Round 1 (2026-11-09)

Verdict: **Changes Requested**

Summary: The money and concurrency logic is correct:
- no negative balance or double debit, because every balance-dependent check runs after `LockAccounts`;
- the lock order is consistent with `ledger.Post`;
- an `InTx` retry re-runs `Begin` from scratch;
- business errors are stored only while the tx is still usable;
- the check order matches the task table.

This round merges my review with an independent reviewer's; every finding was checked in the
code. One **Major**: the table's `503 TRY_AGAIN` row and the new `httpserver` helpers have no
test. The Minors are about making the error mapping fail safe and making tests prove more.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | **Major** | `internal/platform/httpserver/errors.go` (`ErrorBody`, `WriteTryAgain`) | E: every AC row has a test (AC-2) | No test covers `WriteTryAgain` (`ErrTryAgain` → 503 `TRY_AGAIN`) or `ErrorBody`. If it were broken, retry exhaustion would silently become a 500. | Unit tests: a wrapped `ErrTryAgain` (as `InTx` returns it) → 503 + `TRY_AGAIN` + `request_id`; another error → false; `ErrorBody` equals what `WriteError` writes. |
| 2 | Minor | `internal/transfer/http.go` (`render` default, `writeValidationError` default), `service.go` (`isBusinessError`) | Fail-safe error mapping | Two hand-kept lists (`isBusinessError` and `render`), and unknown errors fall into a real business code. A new business error added to one list only would be stored and replayed as `RECIPIENT_LIMIT_EXCEEDED` for the key's lifetime. | One table (error → status, code, message) drives both `isBusinessError` and `render`. Validation mapping gets explicit cases; `default` → 500. |
| 3 | Minor | `internal/transfer/service.go` (frozen check before `LockAccounts`) | PRD-004 §4 vs task step 4 | The sender's frozen status is read without a lock, so a freeze committed during the transfer doesn't stop it. Freezing is a manual SQL step in phase 1 (BR-8), so the impact is low. | Accept and document as a deviation (the task's step order). A row lock on `wallets` would add a second lock per transfer for no phase-1 benefit. |
| 4 | Minor | `cmd/wallet/transfer_integration_test.go` (`OppositeDirectionsNoDeadlock`) | E: tests prove the AC | `InTx` retries 40P01 silently, so "all 201" would pass even with deadlocks. There are also only 2 serial goroutines. | Count `InTx` retries with an observer and assert 0 deadlock retries; raise concurrency. |
| 5 | Minor | tests | E | Missing: a unit test for validation precedence and `NormalizeNote` (Arabic, 140/141); a test that the business-error order wins when two checks fail at once; a check that a FAILED withdrawal is not counted for AC-7. | Add them. |
| 6 | Nit | `api/openapi/wallet.yaml` | API docs | `Idempotent-Replayed` is documented only on 201, but replayed 4xx answers carry it too. A replayed error body keeps the first request's `request_id`. | Document both. |

### Checked and fine
- **A Architecture:** `transfer` imports only the allowed modules + platform (`go list`); limits live in `wallet` (task decision); `GetUserByPhone` now uses the tx, so there's no second pool connection per request.
- **B Money:**
  - `LockAccounts` locks both user accounts in ID order before checks 12–14, and `Post` touches only those rows.
  - The idempotency key is per user and key, so it can't form a lock cycle.
  - `exceeds` cannot overflow; the Cairo day and `created_at` use the same clock.
- **C Go:** sentinel errors, typed results, a renderer closure that is safe to call again on retry.
- **D API:** checks 1–7 then 8–14 exactly as in the task; OpenAPI lists every code (catalogue test).
- **E Tests:** AC-1/3/4/5/7/8/9/10 have tests; ledger integrity runs in every cleanup.

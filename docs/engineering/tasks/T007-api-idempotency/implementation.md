# T007 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T007-api-idempotency`, stacked on T006 (PR #8, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **Migration `000006_idempotency_keys`**: the table as planned (PK `(user_id, key)`, FK to `users`, status CHECK) plus an index on `expires_at`.
- **`internal/idempotency`**:
  - **Model:** `Request`, `Replay`, errors `ErrKeyRequired`, `ErrKeyInvalid`, `ErrKeyReused`, `ErrRequestInProgress` (a plain sentinel), `ErrNotBegun`.
  - **`RequestHash`:** SHA-256 of the decoded struct re-encoded with `encoding/json`.
  - **`Service.Begin`:**
    - `INSERT … ON CONFLICT DO NOTHING`. On a conflict it reads the row: a different hash/method/path → `ErrKeyReused`; `COMPLETED` → `*Replay`; otherwise `ErrRequestInProgress`.
    - SQLSTATE `55P03` on the insert → the unwrapped `ErrRequestInProgress`, so `InTx` does not retry it (TDR-017 §2).
    - An expired row is deleted and re-inserted in the same tx; if another request wins that race, its row is read and compared normally.
  - **`Complete`:** stores status + body + optional transaction ID; returns `ErrNotBegun` if there is no `IN_PROGRESS` row.
  - **`DeleteExpired`:** batched deletes until a short batch.
  - **HTTP helpers:** `KeyFromRequest` (missing → required; > 64 or not a UUID → invalid), `WriteReplay` (`Idempotent-Replayed: true`), `WriteError` (400/400/422/409 with the 4 codes).
  - **`Cleanup`:** a ticker loop that stops on ctx and logs failures; `RunOnce`; a nil logger means discard.
- **Config**: `IDEMPOTENCY_TTL` (24h) and `IDEMPOTENCY_CLEANUP_INTERVAL` (1h), both must be > 0.
- **Wiring**: `api.idempotency` (for T008+); `serve` runs the cleanup in its errgroup.
- **depguard**: `idempotency-deps` (platform only, non-test); probed.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestKeyFromRequest` (missing, v4, v7, v1, "abc", 65 chars) | ✅ |
| AC-2 | `TestRequestHash_Canonical` (key order + whitespace → same; each field change → different) | ✅ |
| AC-3 | `TestBegin_FirstThenReplay` (nil, then `Replay{201, body}`, header set by `WriteReplay`); `TestWriteReplay` | ✅ |
| AC-4 | `TestBegin_Reused` (different hash; same hash with a different path) | ✅ |
| AC-5 | `TestBegin_Replay4xx` (422 replayed) | ✅ |
| AC-6 | `TestBegin_AfterRollback`; `TestComplete_WithoutBegin` | ✅ |
| AC-7 | `TestBegin_ConcurrentSameKey_InProgress` (lock timeout 200 ms, request 1 holds 1 s → `ErrRequestInProgress`, 1 attempt, 0 retries; **wrapping the PgError instead makes this test fail**, checked); `TestBegin_ConcurrentSameKey_Replay` | ✅ |
| AC-8 | `TestBegin_ExpiredRowReplaced`; `TestCleanup_DeletesExpired` (5 expired deleted in batches of 2, live kept; `RunOnce`); `TestCleanup_RunStopsOnCancel` | ✅ |
| AC-9 | `TestConfig_LoadWallet_Defaults` + invalid cases for both variables | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 316 passed, 0 failed |
| Coverage `internal/idempotency` | 85.4% |
| depguard probe | reported; removed |
| `make up` + `make e2e` | ✅; DB at version 6; no WARN/ERROR in the wallet log (cleanup loop running) |
| HTTP | not applicable: no endpoint uses it yet (T008) |

## Deviations from the plan
- **`ErrNotBegun`** was added so `Complete` without a matching `Begin` is an error rather than a silent no-op.
- **`WriteError(w, err) bool`** was added so handlers can map the 4 errors in one call. The codes are catalogued in OpenAPI by T008, when they are first returned by an endpoint (TDR-017 §3).
- **Replayed bodies match semantically, not byte for byte:** stored in `jsonb` as planned, which normalizes key order and whitespace. A replay is the same JSON (tests use `JSONEq`), not necessarily the same bytes.

## Follow-ups (not done)
- None.

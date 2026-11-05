# T007 — API idempotency (keys store + helper)

| Field        | Value |
|--------------|-------|
| ID           | T007 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T007-api-idempotency` |
| Depends on   | T002, T004 |
| PRDs         | PRD-001 BR-16, BR-17, BR-18 |
| TDRs         | TDR-005 L1, TDR-010 §5, TDR-013 |
| Docs changed | new TDR-017 §2 (idempotency lock wait → 409, not retried) |

## What
The reusable idempotency layer that the money endpoints (T008, T017, T018) call **inside their own
`InTx`**, plus the cleanup of expired keys.

Deliverables:
- `idempotency_keys` table.
- `internal/idempotency`: `KeyFromRequest`, `RequestHash`, `Begin`, `Complete`, `Cleanup.Run`.
- Config `IDEMPOTENCY_TTL` (default `24h`) and `IDEMPOTENCY_CLEANUP_INTERVAL` (default `1h`).

## Why
TDR-005 Layer 1 (algorithm, table, 24 h retention, hourly cleanup) and PRD-001 BR-16/17/18.
Decided 2026-11-05: an explicit helper (not middleware), because the key row must be part of the
business DB transaction. A lock wait on the key must answer `409`, not `503` (TDR-010's retry
would otherwise turn it into `TRY_AGAIN`).

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000006_idempotency_keys.up/down.sql` | Table |
| `internal/idempotency/model.go` | `Key`, `Request`, `Replay`, errors |
| `internal/idempotency/service.go`, `repository.go`, `queries.sql` | Begin / Complete / delete expired |
| `internal/idempotency/http.go` | `KeyFromRequest(r) (string, error)`, `WriteReplay(w, Replay)` |
| `internal/idempotency/cleanup.go` | `Cleanup` goroutine (ticker, injected clock, stops on ctx) |
| `cmd/wallet/app.go` | Start `Cleanup.Run` in the errgroup |
| `internal/platform/config` | 2 new variables (both > 0) |
| `.golangci.yml` | depguard: `idempotency` imports only platform |

### Data (migrations)
```sql
idempotency_keys(user_id uuid NOT NULL REFERENCES users(id), key text NOT NULL,
  method text NOT NULL, path text NOT NULL, request_hash bytea NOT NULL,
  status text NOT NULL CHECK (status IN ('IN_PROGRESS','COMPLETED')),
  response_status int NULL, response_body jsonb NULL, transaction_id uuid NULL,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, key))
INDEX (expires_at)
```

### API
No endpoint of its own. Shared behaviour for money `POST`s:

| Case | HTTP | Code / header |
|------|------|---------------|
| Header `Idempotency-Key` missing or empty | 400 | `IDEMPOTENCY_KEY_REQUIRED` |
| Not a UUID (`uuid.Parse`), or > 64 chars | 400 | `IDEMPOTENCY_KEY_INVALID` |
| Same key, different hash (or different method/path) | 422 | `IDEMPOTENCY_KEY_REUSED` |
| Same key, still running in another request | 409 | `REQUEST_IN_PROGRESS` |
| Same key, completed | original status + body | `Idempotent-Replayed: true` |

### Behaviour and design notes
```text
func KeyFromRequest(r *http.Request) (string, error)          // ErrKeyRequired / ErrKeyInvalid
func RequestHash(v any) ([]byte, error)                       // sha256(json.Marshal(v)) of the DECODED request struct
func (s *Service) Begin(ctx, tx, Request{UserID, Key, Method, Path, Hash}) (*Replay, error)
func (s *Service) Complete(ctx, tx, userID, key string, status int, body any, txnID *uuid.UUID) error
```
- **Handler flow** (inside one `InTx`): decode + validate body → `KeyFromRequest` → `RequestHash`
  → `Begin`. If a `*Replay` comes back, return it as-is. Otherwise do the business work, then
  `Complete` with the response (success **or** a 4xx business error). Commit, then write the response.
  - Body/key validation errors (400) happen **before** `Begin` and are not stored.
  - Business 4xx (e.g. `INSUFFICIENT_FUNDS`) are stored with `Complete`, so the same key returns
    the same error (TDR-005 step 3). That means the business code must return the error to the
    handler **without** aborting the DB tx (do the checks before any write that could fail).
  - 5xx / `ErrTryAgain` / panic → the tx rolls back and the key disappears; the client may retry.
- **`Begin`**: `INSERT … VALUES (…,'IN_PROGRESS', expires_at = now + TTL) ON CONFLICT (user_id, key) DO NOTHING`.
  - Inserted → return `nil, nil`.
  - Conflict → `SELECT` the row: different `request_hash`/`method`/`path` → `ErrKeyReused`;
    `COMPLETED` → `&Replay{Status, Body}`; `IN_PROGRESS` (only possible if a future design commits
    it early) → `ErrRequestInProgress`.
  - The insert waits on another uncommitted insert of the same key. If that wait hits
    `lock_timeout` (`55P03`), `Begin` returns `ErrRequestInProgress` — **a plain sentinel, not
    wrapping the `*pgconn.PgError`** — so `InTx` does not retry it, and the handler answers `409`.
    (TDR-005 already says "on timeout the second request gets `409`"; TDR-017 §2 records how this fits TDR-010.)
  - An **expired** row (still present before cleanup) counts as absent: `Begin` deletes it in the
    same tx and inserts a fresh one.
- **`Complete`**: `UPDATE … SET status='COMPLETED', response_status, response_body, transaction_id`.
- **Hash**: the canonical form is the decoded, validated request struct encoded with
  `encoding/json` (fixed field order, no whitespace). `{"a":1, "b":2}` and `{"b":2,"a":1}` hash the same.
- **Cleanup**: every `IDEMPOTENCY_CLEANUP_INTERVAL`, `DELETE … WHERE expires_at < now() LIMIT`-style
  in batches of 1000 until none are left; errors are logged and it tries again next tick.
  It stops when ctx ends.
- The transaction-level guard (`UNIQUE (initiator_user_id, idempotency_key)`, T006) stays forever.

### Out of scope
Wiring into an actual endpoint (T008 is the first), idempotency for webhooks (TDR-007, T015).

## Acceptance Criteria
- [ ] AC-1 — `KeyFromRequest`: missing/empty → `ErrKeyRequired`; `"abc"` or 65 chars → `ErrKeyInvalid`; any UUID version → ok.
- [ ] AC-2 — `RequestHash` is the same for equal structs whatever the JSON key order or whitespace was, and differs when any field differs.
- [ ] AC-3 — First `Begin` → nil; then `Complete(201, body)` + commit. A second `Begin` with the same hash → `Replay{201, body}`, and `WriteReplay` sets `Idempotent-Replayed: true`.
- [ ] AC-4 — A second `Begin` with a different hash → `ErrKeyReused`.
- [ ] AC-5 — A stored 4xx (e.g. `422`) is replayed the same way.
- [ ] AC-6 — If the first tx rolls back, a later `Begin` with the same key → nil (fresh start).
- [ ] AC-7 — Two concurrent requests with the same key: while request 1 holds its uncommitted insert longer than `DB_LOCK_TIMEOUT`, request 2 gets `ErrRequestInProgress`, and `InTx` did **not** retry (one attempt, no `db_tx_retries_total` increment). If request 1 commits within the timeout, request 2 gets the replay.
- [ ] AC-8 — An expired row is replaced by a new `Begin`. `Cleanup` deletes expired rows and keeps live ones (fake clock).
- [ ] AC-9 — Config defaults `IDEMPOTENCY_TTL=24h`, `IDEMPOTENCY_CLEANUP_INTERVAL=1h`; ≤ 0 → error naming the variable.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | unit | `TestKeyFromRequest` (table) |
| AC-2 | unit | `TestRequestHash_Canonical` |
| AC-3–AC-6 | integration | `TestBegin_FirstThenReplay`, `TestBegin_Reused`, `TestBegin_Replay4xx`, `TestBegin_AfterRollback` |
| AC-7 | integration | `TestBegin_ConcurrentSameKey_InProgress`, `TestBegin_ConcurrentSameKey_Replay` |
| AC-8 | integration | `TestBegin_ExpiredRowReplaced`, `TestCleanup_DeletesExpired` |
| AC-9 | unit | `TestConfig_Load` extended |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Helper vs middleware; hash canonical form; key format; lock-timeout handling | defaults / middleware | Answered | Explicit helper, defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | `IDEMPOTENCY_TTL=24h`, `IDEMPOTENCY_CLEANUP_INTERVAL=1h` as config (values from TDR-005) | planning | this task |
| 2026-11-05 | Explicit `Begin/Complete` inside the handler's `InTx`; hash = SHA-256 of re-encoded decoded request; key UUID ≤ 64 → else `IDEMPOTENCY_KEY_INVALID`; lock timeout on the key → `ErrRequestInProgress` (not retried) → 409 | Yahya | TDR-017, this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

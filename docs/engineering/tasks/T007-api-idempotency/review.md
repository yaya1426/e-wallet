# Review — T007

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The idempotency layer follows TDR-005 L1 and TDR-017 §2. The key row is part of the
caller's transaction, and a lock-wait on the key is a plain sentinel, so `InTx` doesn't retry it.
That is proven by a test that fails when the error is wrapped. One Minor race in the expired-key
path was fixed during the review; one Nit is accepted as documented. I reviewed this alone,
re-reading every file against the checklist.

| # | Severity | File:line | Rule | Finding | Status |
|---|----------|-----------|------|---------|--------|
| 1 | Minor | `internal/idempotency/service.go` (`Begin`, expired path) | B: idempotency correctness | When two requests replace the same **expired** key at once, the loser of the re-insert returned `ErrRequestInProgress` (409), even if the winner had already completed, instead of a replay or `ErrKeyReused`. | **Fixed**: after a lost re-insert it re-reads the row and goes through the normal compare. Tests pass. |
| 2 | Nit | migration `000006` `response_body jsonb` | TDR-005 "original response" | `jsonb` normalizes key order and whitespace, so a replay is the same JSON but not the same bytes. | **Accepted** as planned (data model says `jsonb`); documented in `implementation.md`. Clients parse JSON. |

### Checked and fine
- **A Architecture:** `idempotency` imports platform only (probed). The table is owned here. Handlers will call `Begin`/`Complete` inside their own `InTx`, as the task decided (helper, not middleware).
- **B Correctness:**
  - The key insert waits on an uncommitted twin; after it commits, the twin's request gets a replay, and after a rollback it starts fresh (both tested).
  - `55P03` → unwrapped `ErrRequestInProgress` (tested: 1 attempt, 0 retries).
  - Business 4xx responses can be stored (tested with 422).
  - `Complete` fails loudly without `Begin`.
  - Cleanup deletes in batches, uses the injected clock, and stops on ctx.
- **C Go:** sentinel errors, consumer-side `Querier`, the `int32` conversion annotated, a nil logger handled, the goroutine owned by `serve`'s errgroup.
- **D Data/API:** the migration matches the task. The 4 error codes and statuses match the task table; they are catalogued in OpenAPI with T008.
- **E Tests:** every AC is covered, including the concurrency cases and the expired-row replacement; config defaults and invalid values are tested.

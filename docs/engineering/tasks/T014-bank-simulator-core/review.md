# Review — T014

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The simulator meets the task and TDR-008 §2–3 / TDR-007 §1, §4:
- create is serialized under one mutex, so concurrent requests with the same key cannot create two operations;
- the replay, reused-key and scenario order is right (validation first, so a bad request does not consume an override);
- the status changes before the first webhook;
- retries keep the `event_id` and re-sign;
- goroutines stop with ctx.

The compatibility test with the T013 adapter proves both sides agree on the contract. The review found no Blocker, Major or Minor issues, only two Nits.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/banksim/store.go` | C "no unbounded …" | The answer and operation maps grow without limit. This is acceptable for a test double that is restarted often (TDR-008 §2). | None now; phase 2 may add a cap or the JSON snapshot. |
| 2 | Nit | `internal/banksim/server.go` (`setScenario`) | Security | `/admin/scenarios` has no auth. The simulator is test tooling and must never face real users (stated in `cmd/banksim`). | If it is deployed on a public URL, protect or disable `/admin`. |

### Checked and fine
- **A Architecture:** depguard `banksim-deps` is probed; only platform packages are used (`clock`, `config`, `httpserver`, `id`, `logger`).
- **B Correctness:**
  - Idempotency compares the whole request (kind, key, all fields), not raw bytes.
  - A 422 for `0002` is stored and replayed with no operation; invalid requests store nothing.
  - The webhook body matches TDR-007 §4; the signature format is checked with an independent vector.
- **C Go:**
  - Context-bound timers; `WaitGroup.Go`; response bodies drained and closed.
  - `mustJSON` is used only for values that cannot fail to encode.
- **D API/Config:**
  - The contract documents every code (catalogue test) and the admin endpoint.
  - Config errors name their variables; the dev secrets are marked DEV ONLY.
- **E Tests:**
  - AC-1…9 are covered (AC-9 by the compose curl run); 96.9% coverage, stable over 3 `-race` runs.

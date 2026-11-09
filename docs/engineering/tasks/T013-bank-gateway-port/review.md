# Review — T013

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: The port, adapter, fake and contract match TDR-008 §1 and the task:
- every row of the error mapping table is tested in all three operations;
- timeouts and caller cancellation are temporary;
- bodies are size-limited and always closed;
- no HTTP detail leaks to callers.

The review found no Blocker, Major or Minor issues, only one Nit.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/bank/banksim/client.go` (`create`) | Fail fast | A zero `money.Money` (no currency) would be sent as `currency: ""` and rejected by the bank as `BANK_REJECTED`. Callers build amounts with `money.NewPositive`, so it cannot happen today. | Optional: return an error before the call when the amount is not positive. |

### Checked and fine
- **A Architecture:**
  - `bank/...` imports only `platform/money` (`go list`); the depguard rule `bank-deps` is probed.
  - The adapter lives under `bank/banksim`, separate from the simulator binary.
- **B Correctness:**
  - `Idempotency-Key` and `reference` both carry the transaction ID.
  - Amounts go over the wire as `amount_minor` + `currency`; a non-EGP currency, unknown kind/status or missing fields → `ErrTemporary`.
  - Redirects are not followed.
- **C Go:**
  - `http.NewRequestWithContext`, the client `Timeout`, errors wrapped with `%w` (both `ErrTemporary` and the cause).
  - The duration and result are observed in one deferred place.
  - No PII is logged (no IBAN or holder name in logs).
- **D Data/API:**
  - `banksim.yaml` is valid OpenAPI 3.1 and lists the three operations; the config checks name their variables.
  - `BANK_BASE_URL` is enforced only by `serve`, as decided.
- **E Tests:**
  - AC-1…7 are mapped; the timeout test is bounded (200 ms–1 s); the fake is tested.
  - Coverage is about 96%, stable over 3 `-race` runs.

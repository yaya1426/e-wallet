# Review — T020 (2026-11-09, round 1)
Verdict: Approved
Summary: The e2e suite covers all eight planned scenarios black-box through the public API, with the ledger and in-transit invariant checked after each one; a deliberate breach proved the check fails the right test. The guide was executed as written from a clean stack and matched at every step. `.env.example` is now complete. No findings.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| — | — | — | — | None | — |

## Checked and fine
- **Black-box:** the scenarios use only HTTP. The database is read only for the integrity checks and for the bank reference in scenario 7, which the API doesn't expose; the task allows this.
- **Independence and repeatability:**
  - random phones; `waitFinal` uses a bounded wait (15 s);
  - `waitQuiet` runs before the ledger check;
  - no shared mutable state (the webhook helper takes the secret as a parameter).
- **Webhook test:** signs with a local HMAC, independent of the wallet code (a real external caller), and also checks that a wrong secret gets 401.
- **CI:** `smoke` runs `make up` → `make e2e` (5-minute timeout) → logs on failure → `make down`. The default `E2E_DATABASE_URL` matches compose on CI.
- **Docs:**
  - the guide is accurate (verified by running it) and lists phase-1 limits honestly;
  - README links to it;
  - every config variable is documented and the dev secrets are marked.
- **Static checks:** gofmt, vet (including the `e2e` tag) and golangci-lint report 0 issues.

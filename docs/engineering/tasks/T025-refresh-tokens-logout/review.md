# Review — T025 (2026-11-10, round 1)
Verdict: Approved
Summary: Rotation with reuse detection is implemented as TDR-018 §3 describes. The row lock serializes concurrent refreshes (tested), and the revocation of a reused token's family is committed before the 401. Tokens are stored only as hashes and compared through a unique index. Logout can't affect another user. One Minor.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | `internal/identity/refresh.go` (`Refresh`) | Operations | Old rows are never deleted, and the table grows with every refresh. Already noted as a follow-up. | A small cleanup job, for example deleting rows expired or revoked more than 7 days ago, on the reconciliation scheduler. |

## Checked and fine
- **Security:**
  - The token carries 256 bits of `crypto/rand` entropy.
  - Only the SHA-256 is stored; a fast hash is fine at this entropy.
  - Every failure gets the same 401 and code.
  - The logs never contain tokens.
  - Logout returns 204 whatever the token, so it reveals nothing.
- **Correctness:**
  - `FOR UPDATE` on the presented row, so two concurrent refreshes yield one success.
  - The family revocation is committed even though the answer is an error (the `reused` flag outside `InTx`).
  - Sliding expiry.
  - A disabled user gets 403 and keeps the token.
- **API:** the login response is extended in a backward-compatible way; the OpenAPI spec is updated, including the 429 on register that T024 added.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

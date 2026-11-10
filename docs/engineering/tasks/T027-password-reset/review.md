# Review — T027 (2026-11-10, round 1)
Verdict: Approved
Summary: The reset follows TDR-018 §4. Codes are stored hashed and bound to the user, compared in constant time and locked during the check. Attempts are committed even on failure. One error covers every failure, so nothing is revealed. A successful reset revokes all sessions. No findings.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| — | — | — | — | None | — |

## Checked and fine
- **Brute force:** 10⁶ codes, at most 5 tries per code, plus the shared per-phone and per-IP rate limit.
- **Timing:** the bcrypt hash is computed before the transaction, so the row lock is short.
- **Logs:** nothing sensitive is logged.
- **Tests:** cover every AC with a fake clock.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

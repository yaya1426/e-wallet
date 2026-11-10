# Review — T024 (2026-11-10, round 1)
Verdict: Approved
Summary: The client IP is resolved once, in the platform layer, with a safe fallback when the header is missing or invalid. Both limits use it, and the register limit sits in the same place in the handler as the login limit (after decoding). The configurable limit is documented as a deviation and keeps the decided default. No findings.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| — | — | — | — | None | — |

## Checked and fine
- **Spoofing:** the header is only trusted when configured, and DigitalOcean's edge sets `do-connecting-ip` itself. Locally the connection host is used.
- **No PII in logs:** only the IP is logged, which is already common practice.
- **Tests:** cover every resolution case and both limits with a fake clock.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

# Review — T028 (2026-11-10, round 1)
Verdict: Approved
Summary: The health summary is built from each module's own counts, with the rules from the task. The "never ran" rule avoids false warnings right after a deploy. The e2e remote mode skips only what can't be reached, and says so. The production registration limit was kept: the remote run is a documented subset. One Minor.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Minor | `cmd/wallet/app.go` (`newHandler` mounts `GET /metrics`) | Security (operations) | `/metrics` is public on DigitalOcean. It exposes request counts and queue depths: no personal data, but operational detail. Out of this task's scope; recorded as a follow-up. | Protect it with the internal key, or scrape it from inside the platform only. |

## Checked and fine
- **Status rules:** match the task, tested for ok, warning (FAILED run, late job, WARNING discrepancy) and critical.
- **No cross-module SQL:** each count lives in its owner module, and the handler is in the composition root.
- **e2e:** skips state their reason; the login retry is bounded to 90 s; the internal key has no default when remote.
- **DigitalOcean:** the uptime check exists, and the live checks are recorded.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

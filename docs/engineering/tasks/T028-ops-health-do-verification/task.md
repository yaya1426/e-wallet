# T028 — Ops health endpoint, DigitalOcean checks and remote e2e

| Field        | Value |
|--------------|-------|
| ID           | T028 |
| Status       | Done |
| Created      | 2026-11-10 |
| Branch       | `task/T028-ops-health-do-verification` |
| Depends on   | T024–T027 |
| PRDs         | PRD-001 §9 (works end to end) |
| TDRs         | TDR-018 §5, TDR-009 |
| Docs changed | `docs/engineering/manual-testing.md` (new endpoints, DO section) |

## What
- `GET /internal/v1/health/ops` summarises what needs an operator: open discrepancies, stuck
  transactions, dead/pending outbox and inbox events, last job runs, with an overall status.
- A DigitalOcean uptime check on `/readyz` (doctl).
- The e2e suite gains scenarios for refresh/logout and password reset, runs against a remote URL
  (DB checks skipped when no DB URL is given), and is run against the DigitalOcean deployment.
- The manual guide covers the new endpoints and DigitalOcean.

## Why
Decided 2026-11-10: health endpoint instead of a monitoring stack; remote e2e against DO without
DB checks.

## How
### API
`GET /internal/v1/health/ops` (operator key) → `200`:
```json
{"status":"ok|warning|critical",
 "discrepancies":{"open_critical":0,"open_warning":0},
 "stuck":{"TOPUP":0,"WITHDRAWAL":0},
 "outbox":{"pending":0,"dead":0},
 "inbox":{"pending":0,"dead":0},
 "jobs":[{"name":"pending_bank_ops","last_status":"SUCCESS","last_finished_at":"…"}]}
```
Status: `critical` if open CRITICAL discrepancies > 0 or dead outbox/inbox > 0; else `warning` if
open WARNING > 0, any stuck > 0, or a registered job's last run FAILED or is older than 3× its
interval (or missing while `RECON_ENABLED`); else `ok`. Counts come from each owner module
(`discrepancy.CountOpen`, `outbox.Counts`, `webhook.Counts`, `transaction.CountStuck`,
`reconciliation.LastRuns`); `cmd/wallet` assembles them in a small handler (composition root).

### DigitalOcean
- `doctl monitoring uptime create` for `https://<app>/readyz` (name `shark-app readyz`). Alert
  recipients are left to the user (account settings).
- e2e remote mode: `WALLET_URL` set and `E2E_DATABASE_URL` empty → DB-based checks and the
  webhook scenario are skipped (`t.Skip` with the reason); banksim smoke checks skipped when
  `BANKSIM_URL` is unreachable (internal on DO).
- New e2e scenarios: refresh rotation + logout; password reset via the internal API (needs
  `E2E_INTERNAL_API_KEY`, skipped when empty).

### Out of scope
Prometheus/Grafana, managed DB (decided: keep dev DB), alert channels.

## Acceptance Criteria
- [x] AC-1 — Health endpoint returns the counts and the status rules above (integration test covering ok, warning, critical); needs the key.
- [x] AC-2 — e2e: new refresh/logout and password-reset scenarios pass locally and in CI.
- [x] AC-3 — Remote mode skips DB checks cleanly; the suite runs against the DO URL and passes (results in implementation.md).
- [x] AC-4 — Uptime check exists on DO (doctl output recorded).
- [x] AC-5 — Manual guide updated (refresh, logout, password reset, operator API, DO notes) and verified locally.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1 | integration HTTP | `TestInternal_OpsHealth` |
| AC-2 | e2e | `TestE2E_RefreshAndLogout`, `TestE2E_PasswordReset` |
| AC-3, AC-4 | manual against DO | recorded |
| AC-5 | manual | guide walkthrough |

## Open Questions
| # | Question | Options | Status | Answer |
|---|----------|---------|--------|--------|
| 1 | Monitoring | health endpoint / Prometheus stack / later | Answered | Health endpoint + DO uptime check |
| 2 | Remote e2e on DO | skip DB checks / open DB / none | Answered | Run it; skip DB checks |
| 3 | Managed DB | keep dev DB / create cluster | Answered | Keep dev DB for the MVP |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-10 | Health endpoint + uptime check; remote e2e without DB checks; keep dev DB | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-10 | Ready | Planned; all questions answered |
| 2026-11-10 | Approved | Implemented; review round 1: 1 Minor (public /metrics, follow-up) |
| 2026-11-10 | Done | Finalized — PR opened |

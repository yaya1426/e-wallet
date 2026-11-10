# Implementation — T028 Ops health endpoint, DigitalOcean checks and remote e2e

## Summary
- **`GET /internal/v1/health/ops`** (operator key) returns open discrepancies by severity, stuck transactions by type, pending/dead outbox and inbox events, and each reconciliation job's last run and whether it is late. The overall status:
  - `critical`: an open CRITICAL discrepancy, or a dead event;
  - `warning`: an open WARNING discrepancy, a stuck transaction, a FAILED last run, or a late job;
  - `ok` otherwise.

  A job is late when it hasn't run within 3 intervals. A job that has never run only counts as late once the instance has been up for 3 intervals, so a fresh deploy doesn't warn.

  Each module provides its own counts (`discrepancy.CountOpen`, `outbox.Counts`, `webhook.Counts`, `transaction.CountStuck`, `reconciliation.LastRuns`/`Registered`), and `cmd/wallet/ops.go` assembles them.
- **DigitalOcean uptime check** `shark-app readyz` on `https://shark-app-mjjpl.ondigitalocean.app/readyz` (regions eu_west and us_east), created with doctl, ID `814a32b9-199f-4bb8-bcb9-e34602d074d0`. Alert recipients are left to the account owner.
- **e2e remote mode:**
  - with `WALLET_URL` set, there is no default database: without `E2E_DATABASE_URL`, the ledger checks and the webhook scenario are skipped;
  - the simulator's `/healthz` is checked only when `BANKSIM_URL` is given;
  - the operator key comes from `E2E_INTERNAL_API_KEY`, with no default when remote.
- **New e2e scenarios:** `TestE2E_RefreshAndLogout`, `TestE2E_PasswordReset` (which also calls the health endpoint when it's deployed).
- **e2e login retries:** logins now wait and retry on 429, because the login limit is now per real client IP and a fast run from one machine can hit it.
- **Manual guide:**
  - freezing a wallet now goes through the operator API instead of SQL;
  - new sections: 11 (refresh and logout), 12 (password reset), 13 (operator views), and a DigitalOcean section.

  It was run block by block from a clean `make down && make up`: every answer matched, and the final ledger check gave 0/0/0/30000.

## Live verification on DigitalOcean (T024–T027 deployed, merge commit 2a64df0)
The production registration limit (5 per IP per hour) stays in place. Temporarily raising it for the test run was refused as weakening a security control, so the remote run was limited to scenarios that register 5 users in total:

| Test | Result |
|------|--------|
| `TestSmoke_Healthz/Readyz/Metrics/NotFound` | ✅ |
| `TestE2E_Transfer` (funding top-up through banksim, preview, P2P, both histories) | ✅ 4.96 s |
| `TestE2E_Withdrawal/failed_later` (funding top-up, withdrawal reserved, bank fails later, refunded) | ✅ 5.77 s |
| `TestE2E_RefreshAndLogout` | ✅ |
| `TestE2E_PasswordReset` (operator code with the real key) | ✅ |

Live checks of T024 and T026:
- The access log's `client_ip` is the caller's public IP (41.232.167.x, 62 lines) instead of the proxy's.
- The sixth registration this hour → `429`.
- `/internal/v1/discrepancies` without the key → `401`, with the key → `200`.

The health endpoint itself is checked live after this PR deploys.

## Files
`cmd/wallet/ops.go` (+ `ops_integration_test.go`), `app.go`; `internal/{discrepancy,outbox,webhook,reconciliation}` (count queries and methods); `api/openapi/wallet.yaml` (+ test); `test/e2e/{smoke,integrity,helpers,webhook,session}_test.go`; `docs/engineering/manual-testing.md`.

## Acceptance Criteria → evidence
| AC | Evidence | Result |
|----|----------|--------|
| AC-1 | `TestInternal_OpsHealth` (401 without the key; ok on a fresh start; a FAILED run → warning; late jobs → warning; WARNING discrepancy → warning; CRITICAL → critical) | ✅ |
| AC-2 | Local `make e2e`: 14 tests including the two new scenarios | ✅ |
| AC-3 | Remote mode rehearsed locally (DB checks and webhook skipped cleanly), then run against DigitalOcean (table above) | ✅ (subset, see above) |
| AC-4 | Uptime check created (ID above) | ✅ |
| AC-5 | Guide updated and run from a clean stack; all answers as documented | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (after rebasing on T027) | ✅ 0 issues |
| `make e2e` (local) | ✅ |

## Deviations
- The remote run covers a subset of the scenarios because of the production registration limit (explained above). Every MVP flow is covered: top-up, P2P, withdrawal with refund, sessions and password reset.
- The e2e login retry on 429 is an addition, so that runs from one IP are reliable.

## Follow-ups
- `/metrics` is publicly reachable on DigitalOcean. Consider protecting it (internal key or IP allow-list) before real users.
- Configure alert recipients for the uptime check in the DigitalOcean account.

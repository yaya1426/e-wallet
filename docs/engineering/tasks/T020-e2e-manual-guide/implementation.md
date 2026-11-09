# Implementation — T020 End-to-end suite + manual test guide

## Summary
- **`test/e2e`** now runs phase-1 scenarios 1–8 as black-box tests against the compose stack (wallet + banksim + PostgreSQL), next to the T001/T004 smoke tests.
- **Independence:** each scenario registers fresh users with random phones, so runs don't depend on each other and can repeat on the same database.
- **Integrity after every scenario:** `checkIntegrity` waits until no outbox/inbox event is pending, then runs `ledger.VerifyIntegrity` and the in-transit invariant against `E2E_DATABASE_URL`. A broken invariant fails the test that caused it. The tests don't call `t.Parallel`, so a failure points at the right test.
- **CI:** the `smoke` job already ran `make e2e` after `make up`; it now has a comment and a 5-minute timeout on that step.
- **`docs/engineering/manual-testing.md`:** a step-by-step curl + jq walkthrough (top-up, idempotency, P2P, withdrawal, rejections, business rules, freezing a wallet, ledger check) with the expected answer at each step, ending with what phase 1 does not do yet. README links to it.
- **`configs/.env.example`:** now documents every variable the wallet and banksim read, plus the e2e variables.
- **`paymenttest.AssertInTransit`:** now reads both sides in one SQL statement (one snapshot), so it can't flake while other requests run. It takes a `*pgxpool.Pool`; callers were updated.

## Files
| Path | Change |
|------|--------|
| `test/e2e/helpers_test.go` (new) | API client, `scenario` (ready + integrity cleanup), `newUser` (random phone), `linkBank`, `request`, `waitFinal` (15 s), `fund`, `balance`, the test IBANs |
| `test/e2e/integrity_test.go` (new) | `E2E_DATABASE_URL` pool, `checkIntegrity`, `waitQuiet` |
| `test/e2e/topup_test.go`, `transfer_test.go`, `withdrawal_test.go`, `webhook_test.go` (new) | Scenarios 1–7 |
| `.github/workflows/ci.yml` | `smoke`: `make e2e` with `timeout-minutes: 5`; logs on failure and `make down` kept |
| `configs/.env.example` | Added `IDEMPOTENCY_*`, `OUTBOX_*`, `QUEUE_POLL_INTERVAL`, all `LIMIT_*`, and the e2e variables (`WALLET_URL`, `BANKSIM_URL`, `E2E_DATABASE_URL`, `E2E_BANK_WEBHOOK_SECRET`) |
| `docs/engineering/manual-testing.md` (new), `README.md` | Guide + "Try it" section |
| `internal/payment/paymenttest/paymenttest.go` | Single-statement invariant |

## Acceptance Criteria → evidence
| AC | Evidence | Result |
|----|----------|--------|
| AC-1 | `make up && make e2e`: 5 smoke tests and 8 scenario tests (12 cases with subtests) pass in 28.6 s. Each case has its own subtest, so failures are attributed correctly. | ✅ |
| AC-2 | The CI `smoke` job runs `make e2e` on this PR (see the PR checks) | ✅ (see PR) |
| AC-3 | A throwaway test (`zz_breach_test.go`) funded a user, ran `UPDATE ledger_accounts SET balance = balance + 1`, and failed with "ledger integrity broken: … Drifts:[{… Cached:10001 Computed:10000}]". The file was deleted and the row repaired. | ✅ |
| AC-4 | The guide's 12 code blocks were extracted and run in one shell from `make down && make up`. Every answer matched the documented one (balances 0 → 50000 → 37500 → 17500 → 12500 → 17500 → 18500; replay header; error codes; final ledger check 0/0/0/30000). One wording fix: the bank account answer shows `updated_at`, not "timestamps". | ✅ |
| AC-5 | Compose sets every required variable (wallet: `DATABASE_URL`, `JWT_SECRET`, `BANK_BASE_URL`, `BANK_WEBHOOK_SECRET`; banksim: `BANKSIM_WEBHOOK_URL`, `BANK_WEBHOOK_SECRET`), with "DEV ONLY" comments on both secrets. Every `env:` variable in `internal/platform/config` now appears in `.env.example` (checked with a script). | ✅ |

### Scenarios (`go test -tags e2e -v`)
| # | Test | Time |
|---|------|------|
| 1 | `TestE2E_TopUpSuccess`: 0 before confirmation, then COMPLETED / IN, balance 50 000 | 2.4 s |
| 2 | `TestE2E_TopUpRejected/INSUFFICIENT_FUNDS` (…0001), `/INVALID_ACCOUNT` (…0002): FAILED, balance 0 | 2.4 s / 0.6 s |
| 3 | `TestE2E_DoubleClick`: second answer replayed, same id, credited once, one history item | 2.4 s |
| 4 | `TestE2E_Transfer`: preview "Sara H.", 201, balances 37 500 / 12 500, histories OUT / IN with counterparty and note | 2.8 s |
| 5 | `TestE2E_ParallelOverspend`: 20 parallel × 1 000 on 10 000 → 10 × 201, 10 × `INSUFFICIENT_FUNDS`, balances 0 / 10 000 | 2.8 s |
| 6 | `TestE2E_Withdrawal/paid` (COMPLETED, 70 000), `/failed_later` (`ACCOUNT_CLOSED`, back to 100 000), `/refused_at_once` (`INVALID_ACCOUNT`, back to 100 000) | 4.5 / 4.5 / 2.6 s |
| 7 | `TestE2E_DuplicateWebhook`: a signed copy (new `event_id`) → 200 twice, one inbox row DONE, one journal entry, no discrepancy, balance unchanged; a wrong secret → 401 | 2.5 s |
| 8 | `checkIntegrity` in every scenario's cleanup | — |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 issues (a gofmt fix in two test files was needed once) |
| `make e2e` (fresh stack, and again on the stack the guide had used) | ✅ both |
| Throwaway breach (AC-3) and guide walkthrough (AC-4) | ✅ as above |

## Deviations
- The scenarios don't call `t.Parallel()`. The task says they *can* run in parallel, and they are independent, but running them one by one makes the per-test integrity check point at the test that broke it. Scenario 5 is concurrent inside itself.
- `checkIntegrity` first waits for the outbox and inbox to be idle (`waitQuiet`), so background work from the scenario is finished before the ledger is checked.

## Follow-ups
- Phase 2: fault scenarios `0003`–`0009`, crash tests, load tests, reconciliation (the guide's last section).

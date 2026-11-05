# T020 — End-to-end suite + manual test guide

| Field        | Value |
|--------------|-------|
| ID           | T020 |
| Status       | Ready |
| Created      | 2026-11-05 |
| Branch       | `task/T020-e2e-manual-guide` |
| Depends on   | T009, T010, T014, T019 |
| PRDs         | PRD-001 §9 (1, 2 phase-1 part, 3); PRD-002 §8 AC-1–5; PRD-003 §8 AC-1–5; PRD-004 §6 |
| TDRs         | TDR-008 §4, TDR-012 (CI), TDR-015 |
| Docs changed | new `docs/engineering/manual-testing.md`; `README.md` (link) |

## What
Proof that phase 1 is done: an automated black-box suite against the running compose stack, run
in CI, and a step-by-step guide so a person can try the three flows by hand.

Deliverables:
- `test/e2e/` scenarios (build tag `e2e`) on top of the T001 smoke test.
- A small e2e client helper (`test/e2e/client_test.go`): register/login/link/top-up/poll status.
- The CI `smoke` job runs the full e2e suite.
- `docs/engineering/manual-testing.md` (curl + jq walkthrough).
- A final review of compose env (every variable the wallet/banksim need, dev-only secrets marked).

## Why
PRD-001 §9: "all three flows work end-to-end against the bank simulator" and the integrity check
after every scenario. TDR-008 §4: each scenario is an e2e case. Decided 2026-11-05: e2e runs in
the CI `smoke` job and with `make e2e`.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `test/e2e/topup_test.go`, `withdrawal_test.go`, `transfer_test.go`, `webhook_test.go` | Scenarios below |
| `test/e2e/helpers_test.go` | HTTP client, `eventually(cond, 15s)` polling, unique phone per test, the test IBANs |
| `test/e2e/integrity_test.go` | After each test: connect with `E2E_DATABASE_URL` (default `postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable`) and run `ledger.VerifyIntegrity` + the in-transit invariant |
| `.github/workflows/ci.yml` | `smoke` job: `make e2e` already runs `./test/e2e/...`; make sure the stack is up with banksim, give it a 5 min timeout, keep the logs-on-failure step |
| `docker-compose.yml`, `configs/.env.example` | Final review |
| `docs/engineering/manual-testing.md` | Guide |
| `README.md` | Link to the guide |

### Data (migrations)
None.

### API
None (uses the existing ones).

### Behaviour and design notes
- **Test IBANs** (valid mod-97, banksim suffix = scenario):
  `EG890003000100000000123450000` (success), `EG620003000100000000123450001` (rejected by
  webhook), `EG350003000100000000123450002` (rejected synchronously).
- **Scenarios**:
  1. Top-up success → `COMPLETED`, balance up.
  2. Top-up `…0001` → `FAILED INSUFFICIENT_FUNDS`; `…0002` → `FAILED INVALID_ACCOUNT`; balance unchanged.
  3. Double click: the same top-up key twice → one transaction, the second has `Idempotent-Replayed: true`.
  4. P2P: A tops up, sends to B → A and B balances correct; both histories show it with IN/OUT; recipient preview works.
  5. Parallel overspend: A with 10 000 sends 20 parallel P2P of 1 000 → 10 succeed, balance 0.
  6. Withdrawal success → `COMPLETED`; `…0001` → `FAILED ACCOUNT_CLOSED` with the balance restored; `…0002` → `FAILED INVALID_ACCOUNT` with the balance restored.
  7. Duplicate webhook: the test signs and posts a copy of an already-applied `debit.succeeded` (new `event_id`, same reference/amount; dev secret from `E2E_BANK_WEBHOOK_SECRET`) → `200`, balance unchanged.
  8. Integrity check passes after every scenario.
- Tests are independent (a fresh phone per test, `+2010` + 8 random digits) and can run in parallel.
- The guide: start the stack (`make up`), then step by step with `curl` + `jq`: register two users,
  log in (keep the token in a shell variable), link IBANs, top up, watch the status, check the
  wallet, preview + P2P, history, withdraw, then the rejection IBANs, an idempotency replay, and
  freezing a wallet with one SQL line (`docker compose exec postgres psql …`). Each step shows the
  expected answer. It ends with "what phase 1 does not do yet" (lost webhooks / dead outbox → phase 2).

### Out of scope
Fault scenarios `0003`–`0009`, crash tests, load tests (phase 2).

## Acceptance Criteria
- [ ] AC-1 — `make up && make e2e` passes locally with scenarios 1–8.
- [ ] AC-2 — The CI `smoke` job runs the same suite on the PR and passes.
- [ ] AC-3 — A failing integrity check fails the test that caused it (proved once by a throwaway raw `UPDATE` in a local run, recorded in `implementation.md`).
- [ ] AC-4 — Following `manual-testing.md` from a clean `make down && make up` gives the documented answers at every step (done by the implementer, recorded in `implementation.md`).
- [ ] AC-5 — Every env var the wallet and banksim need is set in compose and documented in `.env.example`; dev secrets are clearly marked as such.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-2 | e2e | `TestE2E_TopUp*`, `TestE2E_Transfer*`, `TestE2E_Withdrawal*`, `TestE2E_DuplicateWebhook`, `TestE2E_DoubleClick` |
| AC-3 | manual | throwaway check |
| AC-4 | manual | guide walkthrough |
| AC-5 | review | compose / `.env.example` diff |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Where the e2e suite runs | CI smoke + `make e2e` / local only | Answered | CI smoke + `make e2e` |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | Full e2e in the CI `smoke` job and `make e2e`; guide in `docs/engineering/manual-testing.md` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |

# E-Wallet Documentation

Spec-driven development: **nothing is implemented until the relevant PRD and TDRs are
`Accepted`.** All PRDs and TDRs below were accepted on 2026-11-05 (phase-1 roadmap planning).

## Structure

| Folder | Contents |
|--------|----------|
| [`prds/`](prds/) | Product Requirement Documents — business decisions and rules |
| [`tdrs/`](tdrs/) | Technical Decision Records — final technical decisions |
| [`engineering/`](engineering/) | Architecture (C4) and data model |

## Product Requirements

| ID | Title | Status |
|----|-------|--------|
| [PRD-001](prds/PRD-001-wallet-core.md) | E-Wallet Core (overview, shared rules, limits, NFRs) | Accepted |
| [PRD-002](prds/PRD-002-top-up.md) | Top-up (Bank → Wallet) | Accepted |
| [PRD-003](prds/PRD-003-withdrawal.md) | Withdrawal (Wallet → Bank) | Accepted |
| [PRD-004](prds/PRD-004-p2p-transfer.md) | P2P Transfer (Wallet → Wallet) | Accepted |

## Technical Decisions

| ID | Decision | Status |
|----|----------|--------|
| [TDR-000](tdrs/TDR-000-template.md) | Template | — |
| [TDR-001](tdrs/TDR-001-modular-monolith.md) | Modular monolith, one Go service | Accepted |
| [TDR-002](tdrs/TDR-002-postgresql.md) | PostgreSQL as the only datastore | Accepted |
| [TDR-003](tdrs/TDR-003-money-representation.md) | Money as int64 piasters, no floats | Accepted |
| [TDR-004](tdrs/TDR-004-double-entry-ledger.md) | Double-entry ledger as system of record | Accepted |
| [TDR-005](tdrs/TDR-005-idempotency.md) | Idempotency keys (API, bank, webhooks, ledger) | Accepted |
| [TDR-006](tdrs/TDR-006-outbox-and-workers.md) | Transactional outbox + goroutine worker pool | Accepted |
| [TDR-007](tdrs/TDR-007-webhook-inbox.md) | Bank webhooks through an inbox queue | Accepted |
| [TDR-008](tdrs/TDR-008-bank-simulator.md) | Bank gateway port + bank simulator | Accepted |
| [TDR-009](tdrs/TDR-009-reconciliation-job.md) | Reconciliation jobs, in-process scheduler + advisory lock | Accepted |
| [TDR-010](tdrs/TDR-010-concurrency-control.md) | Row locking & deadlock avoidance | Accepted |
| [TDR-011](tdrs/TDR-011-authentication.md) | Minimal authentication (phone + password, JWT) | Accepted |
| [TDR-012](tdrs/TDR-012-go-project-layout-and-stack.md) | Go project layout & libraries | Accepted |
| [TDR-013](tdrs/TDR-013-ids-and-api-conventions.md) | IDs, time & API conventions | Accepted |
| [TDR-014](tdrs/TDR-014-golang-migrate.md) | Migrations with golang-migrate (supersedes goose in TDR-002/012) | Accepted |
| [TDR-015](tdrs/TDR-015-container-files-at-root.md) | Dockerfile and docker-compose.yml at the repo root | Accepted |
| [TDR-016](tdrs/TDR-016-discrepancy-module.md) | `discrepancy` module owns reconciliation discrepancies | Accepted |
| [TDR-017](tdrs/TDR-017-api-conventions-addendum.md) | API addendum: 413, 429, idempotency lock wait → 409 | Accepted |
| [TDR-018](tdrs/TDR-018-mvp-sessions-and-operator-access.md) | MVP: client IP, register limit, refresh tokens, password reset, operator key | Accepted |
| [TDR-021](tdrs/TDR-021-password-reset-hardening.md) | Password reset hardening: reset ends access tokens, disabled users refused, audit | Accepted (2026-11-12) |

## Engineering

- [Architecture (C4: context → containers → components → code, flows, failure matrix)](engineering/architecture.md)
- [Data model](engineering/data-model.md)
- [Tasks](engineering/tasks/README.md) — one folder per task (`TNNN-<slug>/task.md`)
- [Roadmap](engineering/roadmap.md) — phase-1 task breakdown (top-up, withdrawal, P2P end to end) and phase 2

## Task Workflow (project skills in `.claude/skills/`)

| Step | Skill | Result |
|------|-------|--------|
| 1 | `/plan-new-task <request>` | `task.md` (What / Why / How / AC / Test Plan / Open Questions); PRD/TDR updates after user approval; status `Ready` only with zero open questions |
| 2 | `/implement-task TNNN` | Code + tests; lint, tests, running service and HTTP checks; `implementation.md`; status `In Review` |
| 3 | `/code-review-task TNNN` | Line-by-line review against Go standards and the architecture; `review.md`; `Approved` or `Changes Requested` |
| 4 | `/finalize-task TNNN` | Re-verify, commit, push, open GitHub PR, wait for CI, auto-merge if allowed; status `Done` |

## Open Questions
All answered on 2026-11-05 with the proposed answer (recorded in the PRDs/TDRs):

| # | Question | Answer | Where |
|---|----------|--------|-------|
| Q1 | Final amount / balance / daily limits | Table in PRD-001 §5.6 (config defaults) | PRD-001 §5.6 |
| Q2 | Authentication scope | Phone + password, bcrypt, 15-min JWT | PRD-001 §7, TDR-011 |
| Q3 | Auto-retry rejected withdrawals? | No | PRD-003 WD-BR-6 |
| Q4 | One linked bank account per user? | Yes | PRD-001 §10 |
| Q5 | Reconciliation threshold | 10 min | TDR-009 |
| Q6 | Top-up model | Bank pull / direct debit | PRD-002 |
| Q7 | Recipient preview endpoint | Yes, masked name only | PRD-004 |

## Workflow
1. Review a document → comment / request changes.
2. When agreed, change its status to `Accepted` (TDRs keep their number forever; a changed
   decision gets a **new** TDR that marks the old one `Superseded`).
3. Implementation starts per PRD only when it and its TDRs are `Accepted`.

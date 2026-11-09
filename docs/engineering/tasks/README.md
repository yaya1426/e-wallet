# Tasks

Every unit of work is a task folder `TNNN-<slug>/` (e.g. `T001-project-bootstrap/`) with a
`task.md` inside. Tasks move through four project skills (in `.claude/skills/`):

```
/plan-new-task  →  /implement-task TNNN  →  /code-review-task TNNN  →  /finalize-task TNNN
   Draft → Ready       In Progress → In Review     Approved / Changes Requested     Done (merged)
```

| Status | Set by | Meaning |
|--------|--------|---------|
| `Draft` | plan-new-task | Being planned; has open questions or unapproved doc changes. |
| `Ready` | plan-new-task | What / Why / How / Acceptance Criteria complete, **zero open questions**, doc changes applied. |
| `Blocked` | any skill | Cannot continue; reason written in the task's Status Log. |
| `In Progress` | implement-task | Implementation started. |
| `In Review` | implement-task | Implemented and verified (lint, tests, run); `implementation.md` written. |
| `Changes Requested` | code-review-task | Review found blocking issues; see `review.md`. |
| `Approved` | code-review-task | Review passed. |
| `Done` | finalize-task | PR merged. |

## Files in a task folder

| File | Written by | Contents |
|------|-----------|----------|
| `task.md` | plan-new-task (and status updates by all) | The spec of the task |
| `implementation.md` | implement-task | What was changed, verification results (lint, tests, HTTP checks) |
| `review.md` | code-review-task | Findings with severity and `file:line`, verdict |

## Index

| ID | Title | Status |
|----|-------|--------|
| [T001](T001-project-bootstrap/task.md) | Project bootstrap: Go skeleton, local env and CI | Done |
| [T002](T002-platform-primitives/task.md) | Platform primitives: money, id, clock, unit of work | Done |
| [T003](T003-ledger-core/task.md) | Ledger core (+ sqlc) | Done |
| [T004](T004-onboarding-auth-wallet/task.md) | Onboarding: register, login, my wallet | Done |
| [T005](T005-bank-account-linking/task.md) | Bank account linking | Done |
| [T006](T006-transaction-module/task.md) | Transaction module | Done |
| [T007](T007-api-idempotency/task.md) | API idempotency | Done |
| [T008](T008-p2p-transfer/task.md) | P2P transfer | Done |
| [T009](T009-recipient-preview/task.md) | Recipient preview | Done |
| [T010](T010-transaction-history-api/task.md) | Transaction history API | Done |
| [T011](T011-generic-db-queue/task.md) | Generic DB queue | Done |
| [T012](T012-outbox-module/task.md) | Outbox module | Done |
| [T013](T013-bank-gateway-port/task.md) | Bank gateway port + banksim adapter | Done |
| [T014](T014-bank-simulator-core/task.md) | Bank simulator core | Done |
| [T015](T015-webhook-inbox/task.md) | Webhook ingestion (inbox) | Done |
| [T016](T016-discrepancy-apply-bank-outcome/task.md) | Discrepancy module + ApplyBankOutcome (debits) | Done |
| [T017](T017-top-up-request/task.md) | Top-up request | Done |
| [T018](T018-withdrawal-request/task.md) | Withdrawal request | Done |
| [T019](T019-withdrawal-outcome/task.md) | Withdrawal outcome | Done |
| [T020](T020-e2e-manual-guide/task.md) | End-to-end suite + manual test guide | Ready |

Phase-1 order and dependencies: see the [roadmap](../roadmap.md). Phase-2 tasks are planned after phase 1.

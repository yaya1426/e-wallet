---
name: implement-task
description: Implement a planned e-wallet task (docs/engineering/tasks/TNNN-*/task.md) exactly as specified, but only if it is Ready with no open questions — otherwise block and ask the user. Writes Go code following the TDRs, unit/integration tests covering every business case and acceptance criterion, then verifies with gofmt, go vet, golangci-lint, go test -race, and by running the service and calling the new/changed HTTP endpoints; writes implementation.md and reports results. Use when the user says "implement T001", "/implement-task", or asks to build a planned task.
argument-hint: <task ID, e.g. T003>
---

# implement-task

## 1. Gate — is the task ready? (do this first, write nothing before it passes)
Open `docs/engineering/tasks/<ID>-*/task.md` and check:
1. Status is `Ready` (or `Changes Requested` / `In Progress` when resuming).
2. **What**, **Why**, **How**, **Acceptance Criteria** and **Test Plan** are present and concrete.
3. The **Open Questions** table has no row with status `Open`.
4. Every PRD/TDR the task links exists, and the `Docs changed` edits are present in the docs.
5. Dependencies in `Depends on` are `Done` (or the user explicitly accepts building on top of
   an unmerged one).

If any check fails → **do not implement**. Set status `Blocked` with the reason in the Status
Log, and ask the user the open questions with `AskUserQuestion` (recommended option first).
After the answers: write them into the task (Open Questions → `Answered`, Decisions Log, and
any doc update needed — ask before changing PRDs/TDRs, as in plan-new-task), set `Ready`, and
only then continue.

If something comes up **during** implementation that the task does not answer, stop and ask
the same way. Never fill a gap with an assumption.

## 2. Prepare
- Read the task, its linked PRD sections and TDRs, `docs/engineering/architecture.md` §3–§4
  and `data-model.md`. Read the existing code of the modules you will touch.
- Git: work on branch `task/<ID>-<slug>` created from an up-to-date `main`
  (`git switch -c …`). If the folder is not a git repository yet, ask the user before running
  `git init`.
- Set status `In Progress` + Status Log line.

## 3. Implement (follow the docs, not habits)
Non-negotiables from the TDRs:
- Layout and module boundaries: TDR-012, architecture §3.1–3.2. A module touches only its own
  tables; other modules are used through their exported service API.
- Money: `int64` piasters via `internal/platform/money`; no `float32/float64` (TDR-003).
- Ledger: only `ledger.Post` writes postings; entries balance; append-only (TDR-004).
- One DB transaction (`db.InTx`) for state change + ledger + outbox; **no network call inside
  a DB transaction** (TDR-001 §3, TDR-010 §6).
- Locks in ascending account-ID order, `lock_timeout`, retry on 40001/40P01 (TDR-010).
- Idempotency layers as TDR-005; bank idempotency key = transaction ID.
- API shapes, error body and codes per TDR-013 and the task's How.
- SQL lives in `queries.sql` → `sqlc generate`; migrations via golang-migrate in `migrations/` (TDR-014).
- `context.Context` first parameter; errors wrapped with `%w`; `slog` structured logs;
  goroutines always have a cancellation path.
- Keep the change inside the task's scope. Anything extra → note it as a follow-up, do not do it.

## 4. Tests (required, part of the implementation)
- One or more tests for **every Acceptance Criterion**, named so the AC is recognizable
  (e.g. `TestWithdrawal_InsufficientFunds_Rejected`).
- Cover the business cases: happy path, each business-rule rejection, boundary values
  (min/max amounts, exact balance), and the relevant failure cases: duplicate request
  (idempotency), concurrent requests (`-race`, parallel goroutines), bank technical error vs
  rejection, duplicate/late webhook, crash between steps (simulate by not committing / restarting
  the worker).
- Unit tests use fakes (e.g. in-process `bank.Gateway` fake, fixed `clock.Clock`); table-driven
  where natural.
- Integration tests (`//go:build integration`) run against real Postgres via testcontainers and
  assert ledger integrity at the end (TDR-004 §6).
- Never weaken or skip existing tests.

## 5. Verify
Follow [`references/verification.md`](references/verification.md) **completely**: tidy, gofmt,
vet, golangci-lint, build, unit + integration tests with `-race`, then **run the service** and
exercise every new/changed endpoint over HTTP (happy path + every error code in the AC; for
async flows wait for the terminal status), then the ledger integrity check.
Fix and re-run everything until all steps pass. Report missing tools honestly.

## 6. Record and report
- Write `docs/engineering/tasks/<ID>-*/implementation.md`:
  - Summary of the change; files/modules touched; migrations added.
  - AC → test mapping table with result (✅/❌).
  - Verification table: each step, command, result, key output (lint count, test counts,
    coverage of changed packages).
  - HTTP checks: request, response status/body excerpt, expected vs actual.
  - Deviations from the plan (should be none; any deviation must have been approved by the user).
  - Follow-ups noticed but not done.
- Tick the AC checkboxes in `task.md`; set status `In Review`; Status Log line; update the
  tasks Index.
- Do **not** commit or push (finalize-task does that) unless the user asks.
- Tell the user: what was built, the verification results (pass/fail per step with numbers),
  the HTTP check results, and the next step `/code-review-task <ID>`.

---
name: code-review-task
description: Review the code of an implemented e-wallet task line by line against Go coding standards (Effective Go, Go Code Review Comments), the project's architecture, module boundaries and TDR patterns — naming of functions/variables/packages, package isolation and separation of concerns, abstraction leakage, money/ledger/idempotency/concurrency correctness, tests. Writes review.md with severity-ranked findings (file:line) and a verdict. Use when the user says "review T003", "/code-review-task", or asks to code-review a task.
argument-hint: <task ID, e.g. T003>
---

# code-review-task

The review **reports**; it does not change code unless the user asks for fixes afterwards.

## 1. Preconditions
- Task folder `docs/engineering/tasks/<ID>-*/` exists with `task.md` and `implementation.md`,
  status `In Review` (or `Changes Requested` for a re-review).
- Otherwise stop and tell the user which step is missing (`/implement-task <ID>`).

## 2. Collect the scope
- Changed files: `git diff --stat main...HEAD` plus uncommitted changes
  (`git status --porcelain`, `git diff`). If not a git repo, use the files listed in
  `implementation.md`.
- Read `task.md` (What/How/AC), the linked PRD sections and TDRs, `architecture.md` §3–§4,
  `data-model.md`, and [`references/go-review-checklist.md`](references/go-review-checklist.md).

## 3. Review — file by file, line by line
For **every** changed file, read the whole file (not just the diff hunk, so context and
naming are judged correctly), then go through the checklist sections A–E:
1. **Architecture & boundaries** — placement, import directions, table ownership, isolation of
   packages, separation of concerns, abstraction leakage.
2. **Correctness of money** — ledger postings, transaction boundaries, locks, idempotency,
   state machine, no network calls in DB transactions.
3. **Go standards** — naming (packages, types, functions, variables, receivers, errors),
   errors, context, goroutines, docs, simplicity.
4. **Data & API** — migrations, sqlc queries, API contract vs TDR-013 / OpenAPI.
5. **Tests** — every AC covered, business cases and failure cases, determinism.
6. **Plan conformance** — the code does what `task.md` says, nothing missing, nothing extra.

Also check imports mechanically: `go list -deps ./internal/<module>/...` (or `go list -f
'{{.ImportPath}}: {{.Imports}}' ./internal/...`) against architecture §3.2.
Re-run `gofmt -l .`, `go vet ./...` and `golangci-lint run ./...` to confirm the static state.

For a large change, you may additionally launch a subagent for an independent read of the same
files and checklist (give it the files and the checklist, not your conclusions), then merge
and de-duplicate findings. Only report findings you have verified in the code.

## 4. Write `review.md` in the task folder
```
# Review — <ID> (<date>, round N)
Verdict: Approved | Changes Requested
Summary: 2–3 sentences.

| # | Severity | File:line | Rule (checklist item / TDR) | Finding | Suggested fix |
```
- Severity per the checklist table (Blocker / Major / Minor / Nit).
- Each finding: concrete, with `path/file.go:123`, what is wrong, why (rule), and a fix.
- Also list what was checked and found fine (one line per area), so the review is auditable.
- On re-review, keep previous rounds; mark earlier findings Fixed / Still open.

## 5. Verdict and status
- Any **Blocker** or **Major** open → `Changes Requested`.
- Only Minor/Nit (or nothing) → `Approved` (Minor/Nit listed as optional).
- Update `task.md` status + Status Log, and the tasks Index.

## 6. Report to the user
Verdict, counts per severity, the Blocker/Major findings in short, and the next step:
- `Changes Requested` → ask the user (AskUserQuestion) whether to apply the fixes now. If yes,
  fix, re-run the full verification (`.claude/skills/implement-task/references/verification.md`),
  update `implementation.md`, then review again (round N+1).
- `Approved` → next step `/finalize-task <ID>`.

---
name: finalize-task
description: Finalize an approved e-wallet task — re-verify everything (tidy, gofmt, vet, golangci-lint, build, tests, running service), commit on the task branch, push, open a GitHub pull request with the GitHub CLI, wait for CI/CD to pass, and merge it automatically when the repository allows it. Use only when the user runs "/finalize-task <ID>" or explicitly asks to finalize / ship / open the PR for a task.
argument-hint: <task ID, e.g. T003> [--no-merge]
disable-model-invocation: true
---

# finalize-task

Running this skill is the user's authorization to **commit, push, open the PR and merge it**
for this one task (unless `--no-merge` is passed). It never authorizes anything beyond that
task's PR.

## 1. Preconditions — stop and tell the user if any fails
1. `docs/engineering/tasks/<ID>-*/review.md` latest round says `Verdict: Approved` and
   `task.md` status is `Approved`.
2. Git repository with a GitHub remote: `git remote get-url origin`, `gh auth status`,
   `gh repo view --json nameWithOwner,defaultBranchRef`.
3. Current branch is `task/<ID>-<slug>` (not the default branch).
4. The changes in the working tree belong to this task only (`git status`, `git diff --stat
   <default>...HEAD`). Unrelated changes → ask the user what to do; never include them silently.
5. CI exists: `.github/workflows/` has a workflow that runs on `pull_request`. If not, stop:
   merging without CI is not allowed — suggest planning a CI task.

## 2. Final verification
Run the full procedure in `.claude/skills/implement-task/references/verification.md` again on
the final code. Everything must pass. If something fails: fix it only if the fix is trivial
and inside the task scope (then note it in `implementation.md`); otherwise set the task to
`Changes Requested`, explain, and stop.

## 3. Commit and push
- Update `task.md`: status `Done`, Status Log line "Finalized — PR opened"; update the tasks
  Index in `docs/engineering/tasks/README.md`. (If the PR ends up not merged, step 6 sets the
  status back.)
- Stage only the task's files (`git add <paths>`; no `git add -A` of unknown files).
- Commit message (Conventional Commits):
  ```
  <type>(<module>): <task title> (<ID>)

  <2–4 lines: what and why, from task.md>

  Task: docs/engineering/tasks/<ID>-<slug>/task.md
  ```
  `type` ∈ feat, fix, refactor, test, docs, chore, ci. End with the commit attribution lines the
  session requires, if any.
- `git push -u origin task/<ID>-<slug>`.

## 4. Open the pull request
`gh pr create --base <default-branch> --head task/<ID>-<slug> --title "<ID>: <task title>" --body-file <tmp>`
Body:
```
## What
## Why            (PRD / TDR links)
## How            (modules, migrations, API changes)
## Acceptance Criteria   (checked list from task.md)
## Verification   (table from implementation.md: lint, tests counts, coverage, HTTP checks)
## Review         (link to review.md, verdict)
Task: docs/engineering/tasks/<ID>-<slug>/task.md
```
End the body with the PR attribution line the session requires, if any.
Give the user the PR link.

## 5. Wait for CI/CD
- If the host app offers its own PR/CI monitoring tools (e.g. the Claude desktop app's PR
  tools), use them as the host instructs instead of polling.
- Otherwise run once, in the background: `gh pr checks <number> --watch --fail-fast`
  (no polling loops).
- **Failed check:** read the logs (`gh run view <run-id> --log-failed`), fix inside the task
  scope, re-run the full verification locally, commit (`fix(<module>): … (<ID>)`), push, and
  wait again. After 2 failed fix rounds, stop and ask the user.

## 6. Merge (skipped with `--no-merge`)
Only when **all** are true:
- every required check is green (`gh pr checks <number>`),
- `gh pr view <number> --json mergeable,mergeStateStatus,reviewDecision` shows `MERGEABLE`
  and state `CLEAN` (or `HAS_HOOKS`), and no required review is missing,
- the user has permission (`gh repo view --json viewerPermission` ∈ WRITE/MAINTAIN/ADMIN).

Then: `gh pr merge <number> --squash --delete-branch`.
Never use `--admin`, never bypass branch protection, never force-push.
If merge is not allowed (protection, required reviews, conflicts): leave the PR open, set the
task status back to `Approved` with the reason in its Status Log (commit + push on the PR
branch), and tell the user exactly what is blocking.

After a merge: `git switch <default-branch> && git pull --ff-only`.

## 7. Report
PR link, CI result per check, merged or not (and why), final task status, and the next task
(if any is `Ready` in the Index).

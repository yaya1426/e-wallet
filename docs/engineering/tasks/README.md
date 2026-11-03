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

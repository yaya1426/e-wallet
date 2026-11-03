# TNNN — <Task title>

| Field        | Value |
|--------------|-------|
| ID           | TNNN |
| Status       | Draft |
| Created      | YYYY-MM-DD |
| Branch       | `task/TNNN-<slug>` |
| Depends on   | TNNN, … (or —) |
| PRDs         | PRD-…, (sections) |
| TDRs         | TDR-…, |
| Docs changed | PRD/TDR/engineering files this task created or updated (or —) |

## What
What will exist when this task is done, in plain words. One paragraph + a bullet list of
deliverables. Must be small enough for one PR.

## Why
The business or technical reason. Link the PRD requirement / TDR decision it implements
(e.g. "PRD-003 WD-BR-1, TDR-004 §3").

## How
### Modules and files
| Module / path | Change |
|---------------|--------|

### Data (migrations)
Tables / columns / constraints / indexes added or changed (or "none").

### API
Endpoints added or changed with request/response and error codes (or "none").

### Behaviour and design notes
Step-by-step logic, transaction boundaries, locks, idempotency, error mapping — whatever
an implementer needs so that **nothing has to be guessed**.

### Out of scope
What this task deliberately does not do.

## Acceptance Criteria
Each item is testable and maps to at least one automated test.

- [ ] AC-1 — Given …, when …, then …
- [ ] AC-2 — …

## Test Plan
| AC | Test type (unit / integration / e2e HTTP) | Test name or scenario |
|----|-------------------------------------------|-----------------------|

## Open Questions
A task can be `Ready` only when this table has no `Open` rows.

| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|

## Decisions Log
Decisions taken while planning (with the user's answer) and the docs they changed.

| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|

## Status Log
| Date | Status | Note |
|------|--------|------|
| YYYY-MM-DD | Draft | Created by plan-new-task |

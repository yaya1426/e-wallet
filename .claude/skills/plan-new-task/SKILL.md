---
name: plan-new-task
description: Plan a new e-wallet task as docs/engineering/tasks/TNNN-<slug>/task.md (What, Why, How, Acceptance Criteria, Test Plan, Open Questions) after consulting the architecture, data model, PRDs and TDRs; updates or adds PRDs/TDRs when the task changes a business or technical decision; never assumes — every doubt becomes a question to the user with a recommendation. Use when the user asks to plan, create, add or spec a new task / feature / change, or says "/plan-new-task".
argument-hint: <what the task should do>
---

# plan-new-task

Turn a request into a task spec that can be implemented **without guessing**. No code is
written by this skill.

## Golden rule: no assumptions
Whenever something is unclear, missing, contradictory, or could be done in more than one
reasonable way (business rule, limit, naming, error code, data shape, library, scope), **stop
and ask the user** with the `AskUserQuestion` tool:
- Put your recommended option **first** and add "(Recommended)" to its label.
- Explain in each option's description the trade-off, and in the question text which
  document/section it affects.
- Up to 4 questions per call; group related ones. Ask again after answers if new doubts appear.
- The user decides. Record every answer in the task's **Decisions Log**.

If the user is not available to answer (or prefers to answer later), write the question in
the **Open Questions** table with status `Open` and keep the task in `Draft`.

## Steps

### 1. Understand the request
Restate the request in one or two sentences to yourself. If even that is ambiguous, ask first.

### 2. Consult the documentation (always, before designing)
Read, in this order, and note what is relevant:
1. `docs/README.md` — index and current open questions.
2. `docs/engineering/architecture.md` — modules, dependency rules (§3.2), flows, failure matrix.
3. `docs/engineering/data-model.md` — tables, owners, invariants.
4. Every PRD in `docs/prds/` that touches the request (read the whole relevant PRD, not a grep hit).
5. Every TDR in `docs/tdrs/` that touches the request (money, ledger, idempotency, outbox,
   webhooks, bank, reconciliation, concurrency, auth, layout, API conventions…).
6. `docs/engineering/tasks/README.md` and existing task folders — to avoid duplicates, find
   dependencies, and pick the next ID.
7. The existing code (if any) in the modules the task will touch, so the How matches reality.

### 3. Check the request against the decisions
For each relevant PRD rule and TDR decision decide: **consistent**, **needs new detail**, or
**conflicts**.
- **Business decision changes / additions** → update the PRD section or create a new
  `PRD-NNN-<slug>.md` (same format as existing PRDs).
- **Technical decision changes** → do **not** rewrite an `Accepted` TDR. Create a new TDR from
  `docs/tdrs/TDR-000-template.md` and mark the old one `Superseded by TDR-NNN`. A `Proposed`
  TDR may be edited in place.
- **Architecture / data model affected** → update `docs/engineering/architecture.md` /
  `data-model.md`.
- Before editing any of these documents, **ask the user** to confirm the decision
  (AskUserQuestion, recommendation first). Only after a yes, edit the docs and list them in the
  task's `Docs changed` field and Decisions Log.
- Keep `docs/README.md` index tables in sync with any new/changed PRD or TDR.

### 4. Size the task
One task = one reviewable PR. If the request is bigger, propose to the user a split into
several tasks (with order and dependencies) and ask which one to plan now. Plan each as its
own folder.

### 5. Create the task folder
- ID: next free number after the highest existing `docs/engineering/tasks/T???-*` (start at
  `T001`), zero-padded to 3 digits.
- Folder: `docs/engineering/tasks/TNNN-<kebab-slug>/` (slug ≤ 5 words, English).
- Copy `templates/task.md` (next to this file) to `task.md` and fill **every** section:
  - **What** — deliverables, small and concrete.
  - **Why** — link the exact PRD rule IDs / TDR sections.
  - **How** — modules and files (respecting TDR-012 layout and architecture §3.2 imports),
    migrations, API contract (TDR-013 conventions, error codes), transaction boundaries,
    locking (TDR-010), idempotency (TDR-005), money as int64 piasters (TDR-003), ledger
    postings (TDR-004), outbox/inbox use (TDR-006/007). Out of scope list.
  - **Acceptance Criteria** — Given/When/Then, each testable. Cover the happy path, every
    business-rule rejection, and the failure cases relevant to the flow (duplicates,
    concurrency, bank errors, crash/retry).
  - **Test Plan** — map every AC to unit / integration (testcontainers Postgres) / e2e HTTP tests.
  - **Open Questions** — anything still unanswered.
- Add the task to the Index table in `docs/engineering/tasks/README.md`.

### 6. Decide the status
- `Ready` only if **all** are true: every section filled, Open Questions has no `Open` row,
  all doc changes are applied, dependencies exist (they may still be in progress).
- Otherwise `Draft`. Add a Status Log line either way.

### 7. Report to the user
Short summary: task ID + folder link, status, docs changed, questions still open (if any), and
the next step (`/implement-task TNNN` when Ready).

## Must not
- Write production or test code.
- Invent limits, error codes, names, or behaviours not in the docs or confirmed by the user.
- Mark a task `Ready` with open questions.
- Silently change a PRD/TDR.

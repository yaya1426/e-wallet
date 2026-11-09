# T005 — Bank account linking

| Field        | Value |
|--------------|-------|
| ID           | T005 |
| Status       | Done |
| Created      | 2026-11-05 |
| Branch       | `task/T005-bank-account-linking` |
| Depends on   | T004 |
| PRDs         | PRD-001 US-6, Q4; PRD-002 §3.3; PRD-003 §3.1 |
| TDRs         | TDR-011, TDR-013, TDR-017 |
| Docs changed | — |

## What
A user links one bank account (IBAN + holder name) and can read it back. Top-up and withdrawal
(T017/T018) read it through `identity.GetBankAccount`.

Deliverables:
- `bank_accounts` table.
- `PUT /v1/me/bank-account`, `GET /v1/me/bank-account`.
- Egyptian IBAN validation (ISO 13616 mod-97).
- `identity.GetBankAccount(ctx, q, userID) (BankAccount, error)` → `ErrBankAccountNotFound`.
- OpenAPI additions.

## Why
PRD-001 US-6 and Q4 (one linked account per user). PRD-002/003 preconditions: a linked bank
account is needed to top up or withdraw. Data model `bank_accounts`.

## How

### Modules and files
| Module / path | Change |
|---------------|--------|
| `migrations/000004_bank_accounts.up/down.sql` | Table below |
| `internal/identity/iban.go` | `NormalizeIBAN(s) (string, error)` |
| `internal/identity/bankaccount.go` | `LinkBankAccount` (upsert), `GetBankAccount` |
| `internal/identity/queries.sql` | upsert + get |
| `internal/identity/http.go` | 2 handlers behind `RequireAuth` |
| `api/openapi/wallet.yaml` | 2 endpoints |

### Data (migrations)
```sql
bank_accounts(id uuid PK, user_id uuid UNIQUE NOT NULL REFERENCES users(id),
  iban text NOT NULL CHECK (length(iban) = 29 AND iban LIKE 'EG%'),
  holder_name text NOT NULL, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)
```

### API
**`PUT /v1/me/bank-account`** `{ "iban": "EG89 0003 0001 0000 0000 1234 5000 0", "holder_name": "Ahmed Mohamed Ali" }`
→ `200 { "iban": "EG890003000100000000123450000", "holder_name", "updated_at" }` (the same for create and replace).

**`GET /v1/me/bank-account`** → `200` with the same body, or `404 BANK_ACCOUNT_NOT_FOUND`.

| Error | HTTP | Code |
|-------|------|------|
| Malformed / unknown fields | 400 | `MALFORMED_REQUEST` |
| IBAN invalid (after removing spaces and uppercasing: not `EG` + 27 digits, or mod-97 ≠ 1) | 400 | `INVALID_IBAN` |
| Holder name after trim < 2 or > 100 runes | 400 | `INVALID_HOLDER_NAME` |
| No / bad token | 401 | `UNAUTHENTICATED` |

Validation order: decode → iban → holder name.

### Behaviour and design notes
- Upsert: `INSERT … ON CONFLICT (user_id) DO UPDATE SET iban, holder_name, updated_at` (the row
  keeps its `id` and `created_at`).
- Replacing is allowed at any time, even while a top-up or withdrawal is in flight: each
  transaction stores its own `bank_account_snapshot` (T017/T018).
- IBAN format for EG: 2 letters + 2 check digits + 25 digits (29 characters in all). The mod-97
  check moves the first 4 characters to the end, maps letters to numbers (A=10…Z=35), and needs
  remainder 1. Use integer chunks (no big-float, no float).
- Plain text storage (no encryption at rest in phase 1). The IBAN is not secret, but it is
  personal data: it must not be logged.

### Out of scope
Several accounts per user, account verification with the bank, deleting the link, encryption at rest.

## Acceptance Criteria
- [x] AC-1 — Given a valid IBAN with spaces/lowercase, then `200` with the normalized IBAN, and the row is stored.
- [x] AC-2 — Given a second PUT with another valid IBAN, then `200`, still one row per user (same `id`, `created_at` unchanged, `updated_at` newer).
- [x] AC-3 — Given IBANs with a wrong checksum, the wrong country, a wrong length, or letters in the BBAN, then `400 INVALID_IBAN`. A bad holder name → `400 INVALID_HOLDER_NAME`.
- [x] AC-4 — `GET` before linking → `404 BANK_ACCOUNT_NOT_FOUND`; after linking → `200`.
- [x] AC-5 — User A cannot see or change user B's account (each call uses only the token's user).
- [x] AC-6 — `GetBankAccount` returns `ErrBankAccountNotFound` for a user with no link.
- [x] AC-7 — The test IBANs `EG890003000100000000123450000`, `EG620003000100000000123450001` and `EG350003000100000000123450002` (used by banksim scenarios, T014/T020) are accepted.

## Test Plan
| AC | Test type | Test name or scenario |
|----|-----------|-----------------------|
| AC-1, AC-3, AC-7 | unit | `TestNormalizeIBAN` (table) |
| AC-1, AC-2 | integration HTTP | `TestPutBankAccount_CreateThenReplace` |
| AC-4, AC-6 | integration | `TestGetBankAccount_NotFoundThenFound` |
| AC-5 | integration HTTP | `TestBankAccount_IsolatedPerUser` |

## Open Questions
| # | Question | Options (recommended first) | Status | Answer |
|---|----------|-----------------------------|--------|--------|
| 1 | Replace while in flight; GET endpoint; validation rules | defaults / block replace while in flight | Answered | Defaults |

## Decisions Log
| Date | Decision | Decided by | Docs updated |
|------|----------|-----------|--------------|
| 2026-11-05 | PUT upsert → 200; GET + `BANK_ACCOUNT_NOT_FOUND`; replace allowed any time; `INVALID_IBAN`, `INVALID_HOLDER_NAME` | Yahya | this task |

## Status Log
| Date | Status | Note |
|------|--------|------|
| 2026-11-05 | Ready | Planned with the phase-1 roadmap; all questions answered |
| 2026-11-09 | In Progress | implement-task started on `task/T005-bank-account-linking`, stacked on T004 (PR #6, auto-merge) |
| 2026-11-09 | In Review | Implemented and verified: make ci ok, 262 tests, curl + e2e ok |
| 2026-11-09 | Approved | Review round 1: Approved (1 Minor fixed, 1 Nit optional) |
| 2026-11-09 | Done | Finalized — PR opened with GitHub auto-merge |

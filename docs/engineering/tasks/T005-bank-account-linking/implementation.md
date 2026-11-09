# T005 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T005-bank-account-linking`, stacked on T004 (PR #6, auto-merge); rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **Migration `000004_bank_accounts`**: `bank_accounts` with a unique `user_id` → `users`, IBAN `CHECK (length = 29 AND LIKE 'EG%')`, holder name and timestamps.
- **`internal/identity/iban.go`**: `NormalizeIBAN` removes spaces, upper-cases, requires `^EG[0-9]{27}$` and an ISO 13616 mod-97 remainder of 1. The mod-97 is computed digit by digit on a small `int`, so there are no big numbers and no floats.
- **`internal/identity/bankaccount.go`**:
  - `LinkBankAccount` (validates the IBAN, then the holder name; upsert `ON CONFLICT (user_id)` keeps `id`/`created_at`).
  - `GetBankAccount(ctx, q, userID)` → `ErrBankAccountNotFound`.
  - `BankAccount` documents that the IBAN is personal data that must not be logged.
- **HTTP**: `PUT /v1/me/bank-account` and `GET /v1/me/bank-account` behind `RequireAuth`, with codes `INVALID_IBAN`, `INVALID_HOLDER_NAME`, `BANK_ACCOUNT_NOT_FOUND`.
- **OpenAPI**: the two operations plus the `BankAccountRequest`/`BankAccount` schemas; the catalogue test covers the new codes.
- **Tests**: migration-version tests now count `migrations/*.up.sql` instead of hard-coding the latest version, so later tasks don't have to edit them.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestNormalizeIBAN` "spaces and lower case"; `TestPutBankAccount_CreateThenReplace`; `TestGetBankAccount_NotFoundThenFound` | ✅ |
| AC-2 | `TestPutBankAccount_CreateThenReplace` (1 row, same `id`, `created_at` unchanged, `updated_at` newer) | ✅ |
| AC-3 | `TestNormalizeIBAN` (checksum, one digit changed, UK IBAN, short, long, letter in BBAN, tab, empty); `TestBankAccount_Errors`; `TestLinkBankAccount_ValidationOrder` | ✅ |
| AC-4 | `TestGetBankAccount_NotFoundThenFound_HTTP` | ✅ |
| AC-5 | `TestBankAccount_IsolatedPerUser` (B can't see A's account; B's PUT leaves A's unchanged) | ✅ |
| AC-6 | `TestGetBankAccount_NotFoundThenFound` (pool and inside `InTx`) | ✅ |
| AC-7 | `TestNormalizeIBAN` banksim scenarios 0000/0001/0002 | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` (tidy, fmt, vet, lint, sqlc-check, build, unit, integration) | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 262 passed, 0 failed |
| Coverage of `identity` (`-coverpkg`) | 85.4% from the HTTP tests, 68.0% from the package's own tests |
| `make up` (host port 5433 here) + `make e2e` | ✅ |
| curl | GET before → 404 `BANK_ACCOUNT_NOT_FOUND`; PUT `eg89 0003 …` → 200 `EG8900…`; PUT replace → 200; GET → 200 new IBAN; bad checksum → 400 `INVALID_IBAN`; holder "B" → 400 `INVALID_HOLDER_NAME`; no token → 401; IBAN in logs: 0; one row per user |

## Deviations from the plan
- None in behaviour. The migration-version tests changed as described in Summary (no lowering of the check).

## Follow-ups (not done)
- None.

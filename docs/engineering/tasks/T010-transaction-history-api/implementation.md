# T010 — Implementation Report

| Field  | Value |
|--------|-------|
| Date   | 2026-11-09 |
| Branch | `task/T010-transaction-history-api`, stacked on T009; rebased onto `main` before its PR |
| Result | All verification steps pass |

## Summary
- **`transaction.ListForWallet`**:
  - One SQL `UNION ALL` of the source-wallet and destination-wallet index scans, each `ORDER BY created_at DESC, id DESC LIMIT n+1`, merged and cut to `limit`.
  - It fetches `limit + 1` rows to know whether there is a next page.
  - The cursor is base64url (no padding) of `{"t": RFC 3339 nano, "id": uuid}`; the next page is rows strictly before `(t, id)`.
- **`transaction.GetVisible`**: only if the caller's wallet is the source or destination.
- **`transaction.Handler`**:
  - `GET /v1/me/transactions?limit&cursor` (default 20, 1–100) and `GET /v1/transactions/{id}`.
  - `direction` is OUT/IN from the caller's wallet; `counterparty` is set only for P2P.
  - Codes: `INVALID_LIMIT`, `INVALID_CURSOR`, `TRANSACTION_NOT_FOUND` (also for a non-UUID or another user's ID).
- **Ports**: `WalletOfUser`, `Parties` (`ByWallet` once per page). `cmd/wallet` adapts `wallet.Service` (new `GetByID`) and `identity.Service` (`GetUser` + `MaskName`) to them, so `transaction` still imports only platform packages.
- **OpenAPI**: both endpoints + `TransactionItem`/`TransactionPage`; codes catalogued.

## Acceptance Criteria → Tests
| AC | Tests | Result |
|----|-------|--------|
| AC-1 | `TestListMyTransactions_DirectionsAndCounterparty` (completed top-up IN, failed top-up IN, withdrawal OUT, P2P sent OUT, P2P received IN, newest first; counterparty only on P2P = the other user; the sender sees the same record as OUT) | ✅ |
| AC-2 | `TestListMyTransactions_Pagination` (45 rows with the **same** `created_at` → 20, 20, 5; none repeated or skipped; `next_cursor` null at the end) | ✅ |
| AC-3 | `TestParseLimit`, `TestDecodeCursor` (round trip + 4 kinds of garbage); `TestListMyTransactions_BadParameters` (HTTP 400s) | ✅ |
| AC-4 | `TestGetTransaction_Visibility` (both P2P sides 200 with their own direction; another user's, unknown, non-UUID → 404) | ✅ |
| AC-5 | failed top-up row shows `FAILED`, `failure_code`, `failure_message`, `completed_at` (in AC-1's test) | ✅ |
| AC-6 | `TestList_BatchesParties` (one `ByWallet` call per page, none for a page without P2P) | ✅ |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ exit 0, lint 0 issues |
| Tests (`-race`) | ✅ 374 passed |
| Coverage `internal/transaction` | 69.7% from the HTTP tests; its own tests cover the rest of the module (T006) |
| `make up` + `make e2e` | ✅ (in the finalize run) |

## Deviations from the plan
- `wallet.Service.GetByID` was added for the `Parties` adapter (wallet → owner).

## Follow-ups (not done)
- The `Parties` adapter does 2 queries per distinct P2P counterparty on a page (≤ 100). The handler side is batched as required. A single SQL join is a possible optimisation later.

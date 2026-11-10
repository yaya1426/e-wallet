# Implementation — T026 Operator API: discrepancies and wallet freeze

## Summary
- **Key check:** `httpserver.RequireInternalKey(key)` compares the `X-Internal-Api-Key` header in constant time; missing or wrong → `401 UNAUTHENTICATED`. `cmd/wallet` mounts `/internal/v1/*` only when `INTERNAL_API_KEY` is set (≥ 32 bytes), and logs "internal API enabled/disabled".
- **`GET /internal/v1/discrepancies`:** filters `status` and `kind`; `limit` 1–100 (default 50); keyset `cursor` on `(created_at, id)`; newest first. Errors: `INVALID_FILTER`, `INVALID_LIMIT`, `INVALID_CURSOR`.
- **`POST /internal/v1/discrepancies/{id}/resolve`:** body `{note}`, 1–500 characters after trimming. Returns the resolved discrepancy. Errors: `INVALID_NOTE`, `404 DISCREPANCY_NOT_FOUND`, `409 ALREADY_RESOLVED`. Money is never touched.
- **`POST /internal/v1/wallets/{wallet_id}/freeze`** and **`/unfreeze`:** idempotent, return `{wallet_id, status}`; `404 WALLET_NOT_FOUND`.
- **Audit:** every operator action is logged at INFO (action, IDs, length of the note).
- **Modules:**
  - `discrepancy` gained `Get`, `Resolve`, and kind and cursor filters on `List`, plus its HTTP handler.
  - `wallet` gained `SetStatus` and the internal routes.
- **Keys:** local compose has a DEV ONLY key. On DigitalOcean a generated 48-character key is set as an encrypted `SECRET` (doctl), never printed. A copy is kept in this session's scratchpad for the T028 remote checks.

## Files
`internal/platform/httpserver/internalkey.go`; `internal/discrepancy/{model,service,http}.go`, `queries.sql` + sqlcgen; `internal/wallet/{service,http}.go`, `queries.sql` + sqlcgen; `internal/platform/config` (`INTERNAL_API_KEY`); `cmd/wallet/app.go`; `api/openapi/wallet.yaml` (+ test: `internal` tag, `internalKey` scheme, 4 paths, `Discrepancy` and `WalletStatus` schemas, 5 codes); `docker-compose.yml`, `configs/.env.example`; tests `cmd/wallet/operator_integration_test.go`, `config_test.go`.

## Acceptance Criteria → tests
| AC | Test | Result |
|----|------|--------|
| AC-1 | `TestInternal_Auth` (no key / wrong key → 401; right key → 200; routes 404 without the config) | ✅ |
| AC-2 | `TestInternal_Discrepancies` (newest first, paging with `next_cursor` then null, kind/status filters, 7 validation cases) | ✅ |
| AC-3 | `TestInternal_Resolve` (note validation ×3; resolved with the trimmed note and time; 409; 404 for an unknown ID and a non-UUID) | ✅ |
| AC-4 | `TestInternal_FreezeUnfreeze` (freeze twice → 200 FROZEN; outgoing → `WALLET_FROZEN`; incoming credited; unfreeze → sending works; 404s) | ✅ |
| AC-5 | `TestConfig_InternalAPIKey`, OpenAPI tests | ✅ |
| AC-6 | Key set on DigitalOcean (SECRET); the live 401/200 check happens after deploy in T028 | ✅ set (verified in T028) |

## Verification
| Step | Result |
|------|--------|
| `make ci` | ✅ 0 issues |
| `make e2e` | ✅ |
| Compose | no key → 401; with the key → `200 {"items":[],"next_cursor":null}` |

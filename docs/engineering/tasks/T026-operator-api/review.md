# Review — T026 (2026-11-10, round 1)
Verdict: Approved
Summary: The operator endpoints sit behind one constant-time key check and are not mounted at all without a key. Each module owns its own operator handler (discrepancy, wallet), and the composition root only mounts them. Resolving never touches money. One Nit.

| # | Severity | File:line | Rule | Finding | Suggested fix |
|---|----------|-----------|------|---------|---------------|
| 1 | Nit | `internal/discrepancy/http.go` (`encodeCursor`/`decodeCursor`) | DRY | The cursor code duplicates `transaction/cursor.go`. Two copies are acceptable, but a third should not be added. | Move it to `platform/httpserver` (or a small `platform/cursor` package) when another paged list appears. |

## Checked and fine
- **Security:**
  - `subtle.ConstantTimeCompare`; an empty configured key never matches.
  - The key is ≥ 32 bytes, stored on DigitalOcean as an encrypted SECRET, and never logged.
  - The `gosec` warning on the header name is a false positive and is marked.
- **Correctness:**
  - `Resolve` uses a guarded `UPDATE … WHERE status = 'OPEN'`, then `Get` to tell 404 from 409.
  - Freeze and unfreeze are idempotent.
  - Paging fetches limit+1 rows to know whether there is a next page.
- **API:**
  - The codes are catalogued, and every path has a 401.
  - `transaction_id`, `resolved_at` and `resolution_note` are nullable in the schema.
- **Static checks:** gofmt, vet and golangci-lint report 0 issues.

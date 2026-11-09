# Review — T010

## Round 1 (2026-11-09)

Verdict: **Approved**

Summary: History and the single-item endpoint match the task. The keyset pagination is correct
even with identical `created_at` values, `transaction` stays free of business imports (ports +
adapters in `main`), and the visibility rule is enforced in SQL. lint shows 0 issues and 374
tests pass. One Minor (performance, recorded as a follow-up) and no Blocker or Major. I reviewed
this alone, re-reading every file.

| # | Severity | File:line | Rule | Finding | Status |
|---|----------|-----------|------|---------|--------|
| 1 | Minor | `cmd/wallet/app.go` (`parties.ByWallet`) | Efficiency | Two queries per distinct counterparty wallet on a page (≤ 100 per page; P2P rows only). The handler makes one batch call, as AC-6 requires. | Accepted for phase 1; follow-up in `implementation.md` (one join query). |

### Checked and fine
- **A:** `transaction` imports platform only; the ports are consumer-defined; the adapters live in the composition root.
- **B:** no money writes; reads on the pool, outside any tx.
- **C:** clear names (`Direction`, `Cursor.Encode`/`DecodeCursor`, `GetVisible`); errors map to the task's codes; no global state.
- **D:**
  - The `(created_at, id) <` row comparison with `LIMIT n+1` on both halves before merging is correct (each half returns at least what the merged page needs).
  - The cursor is opaque and validated.
  - The OpenAPI schemas match the JSON (nullable fields typed as `null`-able).
- **E:** every AC has a test; pagination is tested with a frozen clock; the failed-top-up fields are asserted.

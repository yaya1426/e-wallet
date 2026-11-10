-- name: InsertDiscrepancy :execrows
-- The same open problem for a transaction is recorded once (the partial unique index).
INSERT INTO reconciliation_discrepancies (id, kind, severity, transaction_id, details, status, created_at)
VALUES (@id, @kind, @severity, sqlc.narg(transaction_id), @details, 'OPEN', @now)
ON CONFLICT DO NOTHING;

-- name: ListDiscrepancies :many
-- Newest first; id breaks ties between rows created at the same instant. The cursor is the
-- (created_at, id) of the last row of the previous page (keyset pagination, TDR-013).
SELECT id, kind, severity, transaction_id, details, status, created_at, resolved_at, resolution_note
  FROM reconciliation_discrepancies
 WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
   AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
   AND (NOT @has_cursor::boolean OR (created_at, id) < (@cursor_time::timestamptz, @cursor_id::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT @row_limit;

-- name: GetDiscrepancy :one
SELECT id, kind, severity, transaction_id, details, status, created_at, resolved_at, resolution_note
  FROM reconciliation_discrepancies WHERE id = @id;

-- name: ResolveDiscrepancy :one
-- An operator closed it (TDR-009 §3); money is never changed here.
UPDATE reconciliation_discrepancies
   SET status = 'RESOLVED', resolved_at = @now::timestamptz, resolution_note = @note::text
 WHERE id = @id AND status = 'OPEN'
RETURNING id, kind, severity, transaction_id, details, status, created_at, resolved_at, resolution_note;

-- name: CountOpenBySeverity :many
SELECT severity, count(*)::bigint AS open FROM reconciliation_discrepancies WHERE status = 'OPEN' GROUP BY severity;

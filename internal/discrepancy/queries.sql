-- name: InsertDiscrepancy :execrows
-- The same open problem for a transaction is recorded once (the partial unique index).
INSERT INTO reconciliation_discrepancies (id, kind, severity, transaction_id, details, status, created_at)
VALUES (@id, @kind, @severity, sqlc.narg(transaction_id), @details, 'OPEN', @now)
ON CONFLICT DO NOTHING;

-- name: ListDiscrepancies :many
-- Newest first; id breaks ties between rows created at the same instant.
SELECT id, kind, severity, transaction_id, details, status, created_at, resolved_at, resolution_note
  FROM reconciliation_discrepancies
 WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
 ORDER BY created_at DESC, id DESC
 LIMIT @row_limit;

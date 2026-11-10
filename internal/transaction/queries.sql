-- name: CreateTransaction :one
INSERT INTO transactions (id, type, status, initiator_user_id, source_wallet_id, destination_wallet_id,
                          amount, currency, idempotency_key, bank_account_snapshot, note,
                          created_at, updated_at, completed_at)
VALUES (@id, @type, @status, @initiator_user_id, @source_wallet_id, @destination_wallet_id,
        @amount, @currency, @idempotency_key, @bank_account_snapshot, @note,
        @now, @now, @completed_at)
RETURNING *;

-- name: GetTransaction :one
SELECT * FROM transactions WHERE id = @id;

-- name: GetTransactionForUpdate :one
SELECT * FROM transactions WHERE id = @id FOR UPDATE;

-- name: GetTransactionByBankReference :one
SELECT * FROM transactions WHERE bank_reference = @bank_reference;

-- name: TransitionTransaction :one
-- Moves the row only if it is still in the expected status (optimistic check, T006).
-- A NULL patch field keeps the current value.
UPDATE transactions
   SET status          = @to_status,
       bank_reference  = coalesce(sqlc.narg(bank_reference), bank_reference),
       failure_code    = coalesce(sqlc.narg(failure_code), failure_code),
       failure_message = coalesce(sqlc.narg(failure_message), failure_message),
       updated_at      = @now,
       completed_at    = CASE WHEN @to_status::text IN ('COMPLETED', 'FAILED') THEN @now ELSE completed_at END
 WHERE id = @id AND status = @from_status
RETURNING *;

-- name: SumOutgoingForDay :one
-- Daily outgoing limit (PRD-001 §5.6, PRD-003 WD-BR-7): withdrawals that are not FAILED plus
-- completed P2P sent from the wallet, created in [day_start, day_end).
SELECT coalesce(sum(amount), 0)::bigint
  FROM transactions
 WHERE source_wallet_id = @wallet_id
   AND created_at >= @day_start AND created_at < @day_end
   AND ((type = 'WITHDRAWAL' AND status IN ('PENDING', 'PROCESSING', 'COMPLETED'))
     OR (type = 'P2P' AND status = 'COMPLETED'));

-- name: SumPendingTopUps :one
-- Top-ups still waiting for the bank count against the maximum balance (PRD-002 §3.5).
SELECT coalesce(sum(amount), 0)::bigint
  FROM transactions
 WHERE destination_wallet_id = @wallet_id
   AND type = 'TOPUP' AND status IN ('PENDING', 'PROCESSING');

-- name: ListForWallet :many
-- History of a wallet, newest first, keyset-paginated on (created_at, id) (TDR-013). Each half
-- uses its own index (T006); a P2P to oneself cannot exist, so there are no duplicates.
(SELECT * FROM transactions t
  WHERE t.source_wallet_id = @wallet_id
    AND (NOT @has_cursor::bool OR (t.created_at, t.id) < (@cursor_time::timestamptz, @cursor_id::uuid))
  ORDER BY t.created_at DESC, t.id DESC LIMIT @row_limit)
UNION ALL
(SELECT * FROM transactions t
  WHERE t.destination_wallet_id = @wallet_id
    AND (NOT @has_cursor::bool OR (t.created_at, t.id) < (@cursor_time::timestamptz, @cursor_id::uuid))
  ORDER BY t.created_at DESC, t.id DESC LIMIT @row_limit)
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- name: GetVisibleTransaction :one
SELECT * FROM transactions
 WHERE id = @id AND (source_wallet_id = @wallet_id OR destination_wallet_id = @wallet_id);

-- name: ListForReconciliation :many
-- Top-ups and withdrawals waiting for the bank's final answer for longer than expected and
-- not checked recently, oldest first (TDR-009 §2 pending_bank_ops).
SELECT * FROM transactions
 WHERE status = 'PROCESSING' AND type IN ('TOPUP', 'WITHDRAWAL')
   AND created_at < @created_before::timestamptz
   AND (last_reconciled_at IS NULL OR last_reconciled_at < @checked_before::timestamptz)
 ORDER BY created_at, id
 LIMIT @row_limit;

-- name: MarkReconciled :execrows
-- Bookkeeping only: neither the status nor updated_at change.
UPDATE transactions SET last_reconciled_at = @at::timestamptz WHERE id = @id;

-- name: ListStuck :many
-- Top-ups and withdrawals not final long after they were created (TDR-009 stuck_alert).
SELECT * FROM transactions
 WHERE status IN ('PENDING', 'PROCESSING') AND type IN ('TOPUP', 'WITHDRAWAL')
   AND created_at < @created_before::timestamptz
 ORDER BY created_at, id
 LIMIT @row_limit;

-- name: CountStuck :many
SELECT type, count(*)::bigint AS stuck
  FROM transactions
 WHERE status IN ('PENDING', 'PROCESSING') AND type IN ('TOPUP', 'WITHDRAWAL')
   AND created_at < @created_before::timestamptz
 GROUP BY type;

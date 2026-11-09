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

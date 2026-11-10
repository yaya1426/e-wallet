-- name: CreateWallet :exec
INSERT INTO wallets (id, user_id, ledger_account_id, created_at, updated_at)
VALUES (@id, @user_id, @ledger_account_id, @now, @now);

-- name: GetWalletByUserID :one
SELECT * FROM wallets WHERE user_id = @user_id;

-- name: GetWalletByID :one
SELECT * FROM wallets WHERE id = @id;

-- name: SetWalletStatus :one
-- Operator freeze / unfreeze (TDR-018 §5, PRD-001 BR-8).
UPDATE wallets SET status = @status, updated_at = @now WHERE id = @id RETURNING *;

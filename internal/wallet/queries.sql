-- name: CreateWallet :exec
INSERT INTO wallets (id, user_id, ledger_account_id, created_at, updated_at)
VALUES (@id, @user_id, @ledger_account_id, @now, @now);

-- name: GetWalletByUserID :one
SELECT * FROM wallets WHERE user_id = @user_id;

-- name: GetWalletByID :one
SELECT * FROM wallets WHERE id = @id;

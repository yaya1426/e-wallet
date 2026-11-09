-- name: CreateUser :exec
INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
VALUES (@id, @phone_e164, @full_name, @password_hash, @created_at);

-- name: GetUser :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone_e164 = @phone_e164;

-- name: UpsertBankAccount :one
-- One account per user: replacing keeps the row's id and created_at (T005).
INSERT INTO bank_accounts (id, user_id, iban, holder_name, created_at, updated_at)
VALUES (@id, @user_id, @iban, @holder_name, @now, @now)
ON CONFLICT (user_id) DO UPDATE
   SET iban = EXCLUDED.iban, holder_name = EXCLUDED.holder_name, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetBankAccountByUserID :one
SELECT * FROM bank_accounts WHERE user_id = @user_id;

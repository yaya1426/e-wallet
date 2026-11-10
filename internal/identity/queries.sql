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

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, created_at, expires_at)
VALUES (@id, @user_id, @family_id, @token_hash, @created_at, @expires_at);

-- name: GetRefreshTokenForUpdate :one
SELECT * FROM refresh_tokens WHERE token_hash = @token_hash FOR UPDATE;

-- name: MarkRefreshTokenUsed :exec
UPDATE refresh_tokens SET used_at = @used_at::timestamptz WHERE id = @id;

-- name: RevokeRefreshFamily :execrows
UPDATE refresh_tokens SET revoked_at = @now::timestamptz
 WHERE family_id = @family_id AND revoked_at IS NULL;

-- name: RevokeUserRefreshFamilyByToken :execrows
-- Logout: revoke the session of this token, only if it belongs to the user.
UPDATE refresh_tokens SET revoked_at = @now::timestamptz
 WHERE revoked_at IS NULL
   AND family_id = (SELECT r.family_id FROM refresh_tokens r WHERE r.token_hash = @token_hash AND r.user_id = @user_id);

-- name: RevokeUserRefreshTokens :execrows
UPDATE refresh_tokens SET revoked_at = @now::timestamptz WHERE user_id = @user_id AND revoked_at IS NULL;

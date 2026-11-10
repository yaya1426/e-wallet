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

-- name: InvalidateResetCodes :exec
-- Issuing a new code ends the older ones (TDR-018 §4).
UPDATE password_reset_codes SET invalidated_at = @now::timestamptz
 WHERE user_id = @user_id AND used_at IS NULL AND invalidated_at IS NULL;

-- name: InsertResetCode :exec
INSERT INTO password_reset_codes (id, user_id, code_hash, created_at, expires_at)
VALUES (@id, @user_id, @code_hash, @created_at, @expires_at);

-- name: GetActiveResetCodeForUpdate :one
SELECT * FROM password_reset_codes
 WHERE user_id = @user_id AND used_at IS NULL AND invalidated_at IS NULL AND expires_at > @now::timestamptz
 ORDER BY created_at DESC
 LIMIT 1
 FOR UPDATE;

-- name: RecordWrongResetCode :exec
-- A wrong code counts; the last allowed wrong attempt ends the code.
UPDATE password_reset_codes
   SET attempts = attempts + 1,
       invalidated_at = CASE WHEN attempts + 1 >= @max_attempts::int THEN @now::timestamptz ELSE invalidated_at END
 WHERE id = @id;

-- name: MarkResetCodeUsed :exec
UPDATE password_reset_codes SET used_at = @now::timestamptz WHERE id = @id;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = @password_hash WHERE id = @id;

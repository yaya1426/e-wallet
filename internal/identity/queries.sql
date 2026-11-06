-- name: CreateUser :exec
INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
VALUES (@id, @phone_e164, @full_name, @password_hash, @created_at);

-- name: GetUser :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone_e164 = @phone_e164;

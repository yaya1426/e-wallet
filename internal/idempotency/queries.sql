-- name: InsertKey :execrows
-- Waits on another uncommitted insert of the same key (lock_timeout applies); a committed
-- row makes it insert nothing.
INSERT INTO idempotency_keys (user_id, key, method, path, request_hash, status, created_at, expires_at)
VALUES (@user_id, @key, @method, @path, @request_hash, 'IN_PROGRESS', @now, @expires_at)
ON CONFLICT (user_id, key) DO NOTHING;

-- name: GetKey :one
SELECT * FROM idempotency_keys WHERE user_id = @user_id AND key = @key;

-- name: DeleteExpiredKey :execrows
DELETE FROM idempotency_keys WHERE user_id = @user_id AND key = @key AND expires_at <= @now;

-- name: CompleteKey :execrows
UPDATE idempotency_keys
   SET status = 'COMPLETED', response_status = @response_status, response_body = @response_body,
       transaction_id = @transaction_id
 WHERE user_id = @user_id AND key = @key AND status = 'IN_PROGRESS';

-- name: DeleteExpiredBatch :execrows
DELETE FROM idempotency_keys
 WHERE (user_id, key) IN (SELECT k.user_id, k.key FROM idempotency_keys k
                           WHERE k.expires_at <= @now LIMIT @batch_size);

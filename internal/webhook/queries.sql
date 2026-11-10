-- name: InsertEvent :execrows
-- TDR-007 §1: a duplicate (provider, provider_event_id) inserts nothing (0 rows).
INSERT INTO inbox_events (id, provider, provider_event_id, event_type, payload, status,
                          attempts, next_attempt_at, received_at)
VALUES (@id, @provider, @provider_event_id, @event_type, @payload, 'PENDING', 0, @now, @now)
ON CONFLICT (provider, provider_event_id) DO NOTHING;

-- name: ClaimEvents :many
-- Due PENDING events and PROCESSING events whose lease expired (a crashed worker), oldest
-- first. SKIP LOCKED lets several instances claim side by side (TDR-006 §2, TDR-007 §3).
UPDATE inbox_events
   SET status = 'PROCESSING', locked_by = @owner::text, locked_until = @locked_until::timestamptz, attempts = attempts + 1
 WHERE id IN (SELECT e.id FROM inbox_events e
               WHERE (e.status = 'PENDING' AND e.next_attempt_at <= @now)
                  OR (e.status = 'PROCESSING' AND e.locked_until < @now)
               ORDER BY e.next_attempt_at
               LIMIT @batch_size
               FOR UPDATE SKIP LOCKED)
RETURNING id, event_type, payload, attempts;

-- The Mark* queries change the row only while it is still the same claim: PROCESSING with
-- the attempts the worker saw. A stale mark (the lease expired and another worker
-- re-claimed the event) updates nothing.

-- name: MarkDone :execrows
UPDATE inbox_events
   SET status = 'DONE', processed_at = @now::timestamptz, locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: MarkFailed :execrows
UPDATE inbox_events
   SET status = 'FAILED', processed_at = @now::timestamptz, last_error = @last_error::text,
       locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: MarkRetry :execrows
UPDATE inbox_events
   SET status = 'PENDING', next_attempt_at = @next_attempt_at, last_error = @last_error::text,
       locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: MarkDead :execrows
UPDATE inbox_events
   SET status = 'DEAD', last_error = @last_error::text, locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: Counts :one
-- For the ops health summary (T028).
SELECT count(*) FILTER (WHERE status IN ('PENDING', 'PROCESSING'))::bigint AS pending,
       count(*) FILTER (WHERE status = 'DEAD')::bigint AS dead
  FROM inbox_events;

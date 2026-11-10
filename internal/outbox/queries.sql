-- name: InsertEvent :exec
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status,
                           attempts, next_attempt_at, created_at)
VALUES (@id, @aggregate_type, @aggregate_id, @event_type, @payload, 'PENDING', 0, @now, @now);

-- name: ClaimEvents :many
-- TDR-006 §2: due PENDING events and PROCESSING events whose lease expired (a crashed
-- runner), oldest first. SKIP LOCKED lets several runners claim side by side.
UPDATE outbox_events
   SET status = 'PROCESSING', locked_by = @owner::text, locked_until = @locked_until::timestamptz, attempts = attempts + 1
 WHERE id IN (SELECT e.id FROM outbox_events e
               WHERE (e.status = 'PENDING' AND e.next_attempt_at <= @now)
                  OR (e.status = 'PROCESSING' AND e.locked_until < @now)
               ORDER BY e.next_attempt_at
               LIMIT @batch_size
               FOR UPDATE SKIP LOCKED)
RETURNING id, event_type, payload, attempts;

-- The Mark* queries change the row only while it is still the same claim: PROCESSING with
-- the attempts the runner saw. A stale mark (the lease expired and another runner
-- re-claimed the event) updates nothing.

-- name: MarkDone :execrows
UPDATE outbox_events
   SET status = 'DONE', processed_at = @now::timestamptz, last_error = sqlc.narg(last_error),
       locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: MarkRetry :execrows
UPDATE outbox_events
   SET status = 'PENDING', next_attempt_at = @next_attempt_at, last_error = @last_error::text,
       locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: MarkDead :execrows
UPDATE outbox_events
   SET status = 'DEAD', last_error = @last_error::text, locked_by = NULL, locked_until = NULL
 WHERE id = @id AND status = 'PROCESSING' AND attempts = @attempts;

-- name: Backlog :one
-- With no backlog, oldest is @now (the service reports age 0).
SELECT count(*)::bigint AS pending, COALESCE(min(created_at), @now::timestamptz)::timestamptz AS oldest
  FROM outbox_events
 WHERE status IN ('PENDING', 'PROCESSING');

-- name: ListDead :many
-- DEAD events of the given types, oldest first, for reconciliation (TDR-009 dead_outbox).
SELECT id, aggregate_id, event_type, coalesce(last_error, '')::text AS last_error, attempts, created_at
  FROM outbox_events
 WHERE status = 'DEAD' AND event_type = ANY(@event_types::text[])
 ORDER BY created_at, id
 LIMIT @row_limit;

-- name: ResolveDead :execrows
-- Reconciliation handled a DEAD event: it is finished (DONE) with the note in last_error.
UPDATE outbox_events
   SET status = 'DONE', processed_at = @now::timestamptz, last_error = @note::text
 WHERE id = @id AND status = 'DEAD';

-- name: Counts :one
-- For the ops health summary (T028).
SELECT count(*) FILTER (WHERE status IN ('PENDING', 'PROCESSING'))::bigint AS pending,
       count(*) FILTER (WHERE status = 'DEAD')::bigint AS dead
  FROM outbox_events;

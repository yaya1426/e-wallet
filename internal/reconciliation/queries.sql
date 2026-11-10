-- name: InsertJobRun :exec
INSERT INTO job_runs (id, job_name, started_at, finished_at, status, items_checked, items_fixed, error)
VALUES (@id, @job_name, @started_at, @finished_at, @status, @items_checked, @items_fixed, sqlc.narg(error));

-- name: TryAdvisoryLock :one
-- Session-level lock: it must be released with UnlockAdvisory on the same connection.
SELECT pg_try_advisory_lock(@key::bigint) AS acquired;

-- name: UnlockAdvisory :one
SELECT pg_advisory_unlock(@key::bigint) AS released;

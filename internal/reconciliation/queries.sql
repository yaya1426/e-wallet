-- name: InsertJobRun :exec
INSERT INTO job_runs (id, job_name, started_at, finished_at, status, items_checked, items_fixed, error)
VALUES (@id, @job_name, @started_at, @finished_at, @status, @items_checked, @items_fixed, sqlc.narg(error));

-- name: TryAdvisoryLock :one
-- Session-level lock: it must be released with UnlockAdvisory on the same connection.
SELECT pg_try_advisory_lock(@key::bigint) AS acquired;

-- name: UnlockAdvisory :one
SELECT pg_advisory_unlock(@key::bigint) AS released;

-- name: LastRuns :many
-- The latest run of each job, for the ops health summary (T028).
SELECT DISTINCT ON (job_name) job_name, status, finished_at
  FROM job_runs
 ORDER BY job_name, finished_at DESC;

-- Reconciliation job runs (TDR-009 §1, data-model §3). Owned by the reconciliation module:
-- one row per real run of a job. Ticks skipped because another instance held the job's
-- advisory lock are not recorded (decided 2026-11-10), so SKIPPED is allowed but unused.
CREATE TABLE job_runs (
    id            uuid PRIMARY KEY,
    job_name      text NOT NULL,
    started_at    timestamptz NOT NULL,
    finished_at   timestamptz NOT NULL,
    status        text NOT NULL CHECK (status IN ('SUCCESS', 'FAILED', 'SKIPPED')),
    items_checked int NOT NULL DEFAULT 0,
    items_fixed   int NOT NULL DEFAULT 0,
    error         text NULL
);

CREATE INDEX job_runs_job_started_idx ON job_runs (job_name, started_at DESC);

-- Transactional outbox (TDR-006 §1). Owned by the outbox module. Business code inserts an
-- event in its own DB transaction; the outbox runner delivers it at least once. The archive
-- table and its janitor come in phase 2.
CREATE TABLE outbox_events (
    id              uuid PRIMARY KEY,
    aggregate_type  text NOT NULL,
    aggregate_id    uuid NOT NULL,
    event_type      text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'DONE', 'DEAD')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    locked_by       text NULL,
    locked_until    timestamptz NULL,
    last_error      text NULL,
    created_at      timestamptz NOT NULL,
    processed_at    timestamptz NULL
);

-- The claim query reads only due or leased rows; finished rows stay out of the index.
CREATE INDEX outbox_events_due_idx ON outbox_events (status, next_attempt_at)
    WHERE status IN ('PENDING', 'PROCESSING');

CREATE INDEX outbox_events_aggregate_id_idx ON outbox_events (aggregate_id);

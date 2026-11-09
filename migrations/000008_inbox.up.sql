-- Webhook inbox (TDR-007 §2). Owned by the webhook module. The HTTP handler stores each
-- verified bank event once (UNIQUE (provider, provider_event_id)); inbox workers process it
-- at least once. payload keeps the raw body for audit (NFR-9).
CREATE TABLE inbox_events (
    id                uuid PRIMARY KEY,
    provider          text NOT NULL,
    provider_event_id text NOT NULL,
    event_type        text NOT NULL,
    payload           jsonb NOT NULL,
    status            text NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'DONE', 'FAILED', 'DEAD')),
    attempts          int NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL,
    locked_by         text NULL,
    locked_until      timestamptz NULL,
    last_error        text NULL,
    received_at       timestamptz NOT NULL,
    processed_at      timestamptz NULL,
    CONSTRAINT inbox_events_provider_event_key UNIQUE (provider, provider_event_id)
);

-- The claim query reads only due or leased rows; finished rows stay out of the index.
CREATE INDEX inbox_events_due_idx ON inbox_events (status, next_attempt_at)
    WHERE status IN ('PENDING', 'PROCESSING');

-- API idempotency keys (TDR-005 layer 1). Owned by the idempotency module. Rows live at
-- least IDEMPOTENCY_TTL (24 h) and are then deleted by the cleanup job.
CREATE TABLE idempotency_keys (
    user_id         uuid NOT NULL REFERENCES users (id),
    key             text NOT NULL,
    method          text NOT NULL,
    path            text NOT NULL,
    request_hash    bytea NOT NULL,
    status          text NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED')),
    response_status int NULL,
    response_body   jsonb NULL,
    transaction_id  uuid NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

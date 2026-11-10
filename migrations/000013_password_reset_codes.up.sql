-- Operator-assisted password reset (TDR-018 §4). Owned by the identity module. Only a hash of
-- the 6-digit code is stored; a code is valid 15 minutes and for at most 5 wrong attempts.
CREATE TABLE password_reset_codes (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES users (id),
    code_hash      bytea NOT NULL,
    created_at     timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    attempts       int NOT NULL DEFAULT 0,
    used_at        timestamptz NULL,
    invalidated_at timestamptz NULL
);

CREATE INDEX password_reset_codes_user_idx ON password_reset_codes (user_id, created_at DESC);

-- Refresh tokens (TDR-018 §3). Owned by the identity module. Only the SHA-256 of a token is
-- stored. Rows of one login share family_id; reusing a used token revokes the family.
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id),
    family_id  uuid NOT NULL,
    token_hash bytea NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz NULL,
    revoked_at timestamptz NULL,
    CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

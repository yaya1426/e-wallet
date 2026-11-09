-- One linked bank account per user (PRD-001 US-6, Q4). Owned by the identity module.
CREATE TABLE bank_accounts (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id),
    -- Normalized Egyptian IBAN: EG + 2 check digits + 25 digits (validated in Go, mod-97).
    iban        text NOT NULL CHECK (length(iban) = 29 AND iban LIKE 'EG%'),
    holder_name text NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    CONSTRAINT bank_accounts_user_id_key UNIQUE (user_id)
);

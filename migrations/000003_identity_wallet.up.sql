-- Users (identity module, TDR-011) and their wallets (wallet module, PRD-001 BR-5).

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    phone_e164    text NOT NULL,
    full_name     text NOT NULL,
    password_hash text NOT NULL,
    status        text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at    timestamptz NOT NULL,
    CONSTRAINT users_phone_e164_key UNIQUE (phone_e164)
);

-- Exactly one EGP wallet per user, backed by one USER_WALLET ledger account (TDR-004 §2).
CREATE TABLE wallets (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users (id),
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts (id),
    currency          char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
    status            text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'FROZEN')),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT wallets_user_id_key UNIQUE (user_id),
    CONSTRAINT wallets_ledger_account_id_key UNIQUE (ledger_account_id)
);

-- Business record of every user action and its state machine (PRD-001 BR-11, BR-14;
-- architecture §5.5). Owned by the transaction module.
CREATE TABLE transactions (
    id                    uuid PRIMARY KEY, -- also the bank idempotency key (TDR-013)
    type                  text NOT NULL CHECK (type IN ('TOPUP', 'WITHDRAWAL', 'P2P')),
    status                text NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    initiator_user_id     uuid NOT NULL REFERENCES users (id),
    source_wallet_id      uuid NULL REFERENCES wallets (id),
    destination_wallet_id uuid NULL REFERENCES wallets (id),
    amount                bigint NOT NULL CHECK (amount > 0),
    currency              char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
    idempotency_key       text NOT NULL,
    bank_reference        text NULL,
    bank_account_snapshot jsonb NULL,
    note                  text NULL CHECK (note IS NULL OR char_length(note) <= 140),
    failure_code          text NULL,
    failure_message       text NULL,
    last_reconciled_at    timestamptz NULL,
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    completed_at          timestamptz NULL,
    -- Kept forever: even after the 24 h API key expires, a late retry cannot create a
    -- second transaction (TDR-005 layer 2).
    CONSTRAINT transactions_initiator_idempotency_key UNIQUE (initiator_user_id, idempotency_key),
    CONSTRAINT transactions_bank_reference_key UNIQUE (bank_reference),
    CONSTRAINT transactions_wallets_by_type CHECK (
           (type = 'TOPUP' AND destination_wallet_id IS NOT NULL AND source_wallet_id IS NULL)
        OR (type = 'WITHDRAWAL' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NULL)
        OR (type = 'P2P' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NOT NULL
            AND source_wallet_id <> destination_wallet_id))
);

CREATE INDEX transactions_open_status_idx ON transactions (status, updated_at)
    WHERE status IN ('PENDING', 'PROCESSING');
CREATE INDEX transactions_source_wallet_idx ON transactions (source_wallet_id, created_at DESC, id DESC);
CREATE INDEX transactions_destination_wallet_idx ON transactions (destination_wallet_id, created_at DESC, id DESC);

-- Every journal entry belongs to a transaction (TDR-004 §1; deferred from T003).
ALTER TABLE journal_entries
    ADD CONSTRAINT journal_entries_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions (id);

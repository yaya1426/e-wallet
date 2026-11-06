-- Double-entry ledger (TDR-004): accounts, journal entries and postings.
-- journal_entries.transaction_id gets its foreign key to transactions in T006.

CREATE TABLE ledger_accounts (
    id          uuid PRIMARY KEY,
    code        text NOT NULL UNIQUE,
    kind        text NOT NULL CHECK (kind IN ('USER_WALLET', 'SYSTEM')),
    type        text NOT NULL CHECK (type IN ('ASSET', 'LIABILITY')),
    normal_side text NOT NULL CHECK (normal_side IN ('DEBIT', 'CREDIT')),
    currency    char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
    -- Cached balance in the account's normal side, kept in the same DB transaction as the postings.
    balance     bigint NOT NULL DEFAULT 0,
    version     bigint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    -- Last line of defence against overdraft (PRD-001 BR-6, TDR-010 §4).
    CONSTRAINT ledger_accounts_user_wallet_non_negative CHECK (kind <> 'USER_WALLET' OR balance >= 0)
);

CREATE TABLE journal_entries (
    id             uuid PRIMARY KEY,
    transaction_id uuid NOT NULL,
    entry_type     text NOT NULL CHECK (entry_type IN ('TOPUP_SETTLE', 'WITHDRAWAL_RESERVE',
                       'WITHDRAWAL_SETTLE', 'WITHDRAWAL_REFUND', 'P2P', 'ADJUSTMENT')),
    description    text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    -- The same business step can never be posted twice (TDR-004 §4, TDR-005 layer 4).
    CONSTRAINT journal_entries_transaction_entry_type_key UNIQUE (transaction_id, entry_type)
);

CREATE TABLE postings (
    id               uuid PRIMARY KEY,
    journal_entry_id uuid NOT NULL REFERENCES journal_entries (id),
    account_id       uuid NOT NULL REFERENCES ledger_accounts (id),
    direction        text NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount           bigint NOT NULL CHECK (amount > 0),
    currency         char(3) NOT NULL DEFAULT 'EGP' CHECK (currency = 'EGP'),
    balance_after    bigint NOT NULL,
    created_at       timestamptz NOT NULL
);

CREATE INDEX postings_account_id_created_at_idx ON postings (account_id, created_at);
CREATE INDEX postings_journal_entry_id_idx ON postings (journal_entry_id);

-- Every journal entry must balance and have at least two postings. Checked at commit
-- (deferred), from both tables, so an entry without postings is caught as well.
CREATE FUNCTION ledger_check_entry_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    entry_id uuid;
    debits   numeric;
    credits  numeric;
    lines    bigint;
BEGIN
    IF TG_TABLE_NAME = 'postings' THEN
        entry_id := NEW.journal_entry_id;
    ELSE
        entry_id := NEW.id;
    END IF;

    SELECT coalesce(sum(amount) FILTER (WHERE direction = 'DEBIT'), 0),
           coalesce(sum(amount) FILTER (WHERE direction = 'CREDIT'), 0),
           count(*)
      INTO debits, credits, lines
      FROM postings
     WHERE journal_entry_id = entry_id;

    IF lines < 2 OR debits <> credits THEN
        RAISE EXCEPTION 'ledger: journal entry % is not balanced (debits %, credits %, postings %)',
            entry_id, debits, credits, lines
            USING ERRCODE = 'check_violation', CONSTRAINT = 'journal_entry_balanced';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER postings_balanced
    AFTER INSERT ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_check_entry_balanced();

CREATE CONSTRAINT TRIGGER journal_entries_balanced
    AFTER INSERT ON journal_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_check_entry_balanced();

-- The ledger is append-only (TDR-004 §1, PRD-001 BR-13).
CREATE FUNCTION ledger_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger: % on % is not allowed; the ledger is append-only', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TRIGGER journal_entries_append_only
    BEFORE UPDATE OR DELETE ON journal_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_append_only();

CREATE TRIGGER journal_entries_no_truncate
    BEFORE TRUNCATE ON journal_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_append_only();

CREATE TRIGGER postings_append_only
    BEFORE UPDATE OR DELETE ON postings
    FOR EACH ROW EXECUTE FUNCTION ledger_append_only();

CREATE TRIGGER postings_no_truncate
    BEFORE TRUNCATE ON postings
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_append_only();

-- System accounts (TDR-004 §2). Fixed IDs keep them identical in every environment.
INSERT INTO ledger_accounts (id, code, kind, type, normal_side, created_at, updated_at) VALUES
    ('00000000-0000-7000-8000-000000000001', 'BANK_SETTLEMENT',        'SYSTEM', 'ASSET',     'DEBIT',  now(), now()),
    ('00000000-0000-7000-8000-000000000002', 'WITHDRAWALS_IN_TRANSIT', 'SYSTEM', 'LIABILITY', 'CREDIT', now(), now()),
    ('00000000-0000-7000-8000-000000000003', 'SUSPENSE',               'SYSTEM', 'LIABILITY', 'CREDIT', now(), now());

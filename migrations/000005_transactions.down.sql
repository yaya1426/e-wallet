-- Local development only (TDR-014): production migrations are forward-only.
ALTER TABLE journal_entries DROP CONSTRAINT IF EXISTS journal_entries_transaction_id_fkey;
DROP TABLE IF EXISTS transactions;

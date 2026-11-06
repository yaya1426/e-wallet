-- Local development only (TDR-014): production migrations are forward-only.
DROP TABLE IF EXISTS postings;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS ledger_accounts;
DROP FUNCTION IF EXISTS ledger_append_only();
DROP FUNCTION IF EXISTS ledger_check_entry_balanced();

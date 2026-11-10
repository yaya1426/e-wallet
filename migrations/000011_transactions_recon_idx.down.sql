-- Local development only (TDR-014): production migrations are forward-only.
DROP INDEX IF EXISTS transactions_processing_created_idx;

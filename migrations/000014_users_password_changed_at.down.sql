-- Local development only (TDR-014): production migrations are forward-only.
ALTER TABLE users DROP COLUMN IF EXISTS password_changed_at;

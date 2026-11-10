-- Local development only (TDR-014): production migrations are forward-only.
DROP TABLE IF EXISTS password_reset_codes;

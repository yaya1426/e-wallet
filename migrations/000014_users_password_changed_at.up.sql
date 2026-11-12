-- Password reset hardening (TDR-021 §1). The last password change; an access token issued
-- before it (JWT iat, whole seconds) is rejected. NULL = the password was never changed.
ALTER TABLE users ADD COLUMN password_changed_at timestamptz NULL;

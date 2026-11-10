-- Reconciliation (TDR-009 §2, T022): pending_bank_ops looks for PROCESSING transactions by age.
CREATE INDEX transactions_processing_created_idx ON transactions (created_at)
    WHERE status = 'PROCESSING';

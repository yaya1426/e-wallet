-- Reconciliation discrepancies (TDR-016, TDR-009 §3). Owned by the discrepancy module: a
-- problem an operator must look at. Recording one never changes money or a transaction.
CREATE TABLE reconciliation_discrepancies (
    id              uuid PRIMARY KEY,
    kind            text NOT NULL CHECK (kind IN ('STATUS_MISMATCH', 'AMOUNT_MISMATCH', 'UNMATCHED_BANK_MOVEMENT',
                        'UNMATCHED_LEDGER_MOVEMENT', 'STUCK_TRANSACTION', 'UNKNOWN_BANK_EVENT', 'LEDGER_INTEGRITY')),
    severity        text NOT NULL CHECK (severity IN ('CRITICAL', 'WARNING')),
    transaction_id  uuid NULL REFERENCES transactions (id),
    details         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'RESOLVED')),
    created_at      timestamptz NOT NULL,
    resolved_at     timestamptz NULL,
    resolution_note text NULL
);

-- One open discrepancy per (transaction, kind): recording the same problem again is a no-op.
CREATE UNIQUE INDEX reconciliation_discrepancies_open_key ON reconciliation_discrepancies (transaction_id, kind)
    WHERE status = 'OPEN' AND transaction_id IS NOT NULL;

CREATE INDEX reconciliation_discrepancies_status_idx ON reconciliation_discrepancies (status, created_at);

package payment

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/transaction"
)

// Failure of a request the bank never received (PRD-002 TU-BR-7, PRD-003 §7; message decided
// 2026-11-10).
const (
	CodeBankUnavailable    = "BANK_UNAVAILABLE"
	messageBankUnavailable = "The bank could not be reached. No money was moved."
)

// FailUnreachable fails a top-up or withdrawal the bank has no record of, after its request
// went DEAD in the outbox (reconciliation's dead_outbox, TDR-009): a top-up becomes FAILED; a
// withdrawal becomes FAILED and its reserve is refunded in the same transaction. Both carry
// BANK_UNAVAILABLE. It locks the transaction; one that is no longer PENDING returns
// transaction.ErrStatusConflict and nothing changes.
func (s *Service) FailUnreachable(ctx context.Context, tx pgx.Tx, txnID uuid.UUID) error {
	txn, err := s.transactions.GetForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	if txn.Status != transaction.StatusPending {
		return &transaction.StatusConflictError{Expected: transaction.StatusPending, Current: txn.Status}
	}
	if txn.Type == transaction.TypeWithdrawal {
		return s.failAndRefund(ctx, tx, txn, CodeBankUnavailable, messageBankUnavailable)
	}
	code, msg := CodeBankUnavailable, messageBankUnavailable
	_, err = s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusFailed,
		transaction.Patch{FailureCode: &code, FailureMessage: &msg})
	return err
}

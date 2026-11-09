package payment

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// failAndRefund fails a withdrawal the bank refused (synchronously or later, T019) and gives
// the reserved money back, in tx: Transition(current status → FAILED, the bank's code and
// message) and WITHDRAWAL_REFUND (Dr WITHDRAWALS_IN_TRANSIT / Cr user wallet, TDR-004 §3).
// txn must be locked by the caller and not terminal. The refund is credited even when the
// wallet is frozen or above the maximum balance (WD-BR-8): the money was the user's.
func (s *Service) failAndRefund(ctx context.Context, tx pgx.Tx, txn transaction.Transaction, code, message string) error {
	if _, err := s.transactions.Transition(ctx, tx, txn.ID, txn.Status, transaction.StatusFailed,
		transaction.Patch{FailureCode: &code, FailureMessage: &message}); err != nil {
		return err
	}
	w, err := s.wallets.GetByID(ctx, tx, txn.SourceWalletID)
	if err != nil {
		return err
	}
	inTransit, err := s.ledger.GetAccountByCode(ctx, tx, ledger.CodeWithdrawalsInTransit)
	if err != nil {
		return err
	}
	if _, err := s.ledger.Post(ctx, tx, ledger.Entry{
		TransactionID: txn.ID,
		Type:          ledger.EntryWithdrawalRefund,
		Description:   "Withdrawal refused by the bank; reserve returned",
		Postings: []ledger.Posting{
			{AccountID: inTransit.ID, Direction: ledger.Debit, Amount: txn.Amount},
			{AccountID: w.LedgerAccountID, Direction: ledger.Credit, Amount: txn.Amount},
		},
	}); err != nil {
		return fmt.Errorf("payment: refund withdrawal %s: %w", txn.ID, err)
	}
	return nil
}

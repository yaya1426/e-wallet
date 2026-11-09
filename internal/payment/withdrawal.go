package payment

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// EventWithdrawalRequested asks the outbox to send the payout to the bank (TDR-006).
const EventWithdrawalRequested = "withdrawal.requested"

// RequestWithdrawal accepts a withdrawal (PRD-003 §4): checks 6–10 under the wallet account
// lock → transaction (WITHDRAWAL, PENDING, with the bank account snapshot) → the reserve
// entry WITHDRAWAL_RESERVE (Dr user wallet / Cr WITHDRAWALS_IN_TRANSIT, WD-BR-1) → outbox
// event withdrawal.requested, in one unit of work with the idempotency key (see request).
// The available balance is lower as soon as it commits (WD-1).
func (s *Service) RequestWithdrawal(ctx context.Context, in RequestInput, render Renderer) (Response, error) {
	return s.request(ctx, in, render, func(ctx context.Context, tx pgx.Tx) (transaction.Transaction, error) {
		w, snapshot, err := s.checkedWallet(ctx, tx, in.UserID) // 6, 7
		if err != nil {
			return transaction.Transaction{}, err
		}
		amount := in.Amount.Amount()
		if err := s.limits.CheckWithdrawalAmount(amount); err != nil { // 8
			return transaction.Transaction{}, err
		}
		// Lock before reading the balance (TDR-010): parallel withdrawals cannot overdraw.
		accounts, err := s.ledger.LockAccounts(ctx, tx, []ledger.AccountID{w.LedgerAccountID})
		if err != nil {
			return transaction.Transaction{}, err
		}
		if accounts[w.LedgerAccountID].Balance.Amount() < amount { // 9
			return transaction.Transaction{}, wallet.ErrInsufficientFunds
		}
		dayStart, dayEnd := clock.CairoDay(s.clock.Now())
		spent, err := s.transactions.SumOutgoingForDay(ctx, tx, w.ID, dayStart, dayEnd)
		if err != nil {
			return transaction.Transaction{}, err
		}
		if err := s.limits.CheckDailyOutgoing(spent, amount); err != nil { // 10
			return transaction.Transaction{}, err
		}

		txn, err := s.transactions.Create(ctx, tx, transaction.New{
			Type: transaction.TypeWithdrawal, Status: transaction.StatusPending, InitiatorUserID: in.UserID,
			SourceWalletID: w.ID, Amount: in.Amount, IdempotencyKey: in.Idempotency.Key, BankAccount: snapshot,
		})
		if err != nil {
			return transaction.Transaction{}, err
		}
		inTransit, err := s.ledger.GetAccountByCode(ctx, tx, ledger.CodeWithdrawalsInTransit)
		if err != nil {
			return transaction.Transaction{}, err
		}
		if _, err := s.ledger.Post(ctx, tx, ledger.Entry{
			TransactionID: txn.ID,
			Type:          ledger.EntryWithdrawalReserve,
			Description:   "Withdrawal reserved until the bank pays out",
			Postings: []ledger.Posting{
				{AccountID: w.LedgerAccountID, Direction: ledger.Debit, Amount: in.Amount},
				{AccountID: inTransit.ID, Direction: ledger.Credit, Amount: in.Amount},
			},
		}); err != nil {
			return transaction.Transaction{}, fmt.Errorf("payment: reserve withdrawal: %w", err)
		}
		return txn, s.enqueueRequested(ctx, tx, EventWithdrawalRequested, txn.ID)
	})
}

// deliverWithdrawalRequested asks the bank to pay out (see deliverRequested). A rejection
// fails the withdrawal and refunds the reserve in the same transaction, if it is still
// PENDING. No new payout is attempted after a rejection (WD-BR-6).
func (s *Service) deliverWithdrawalRequested(ctx context.Context, job queue.Job) queue.Result {
	return s.deliverRequested(ctx, job, bankFlow{
		txnType: transaction.TypeWithdrawal,
		call: func(ctx context.Context, txn transaction.Transaction) (bank.Acceptance, error) {
			return s.bank.CreatePayout(ctx, bank.PayoutRequest{
				IdempotencyKey: txn.ID, Amount: txn.Amount, IBAN: txn.BankAccount.IBAN, HolderName: txn.BankAccount.HolderName,
			})
		},
		reject: func(ctx context.Context, tx pgx.Tx, txn transaction.Transaction, rejected *bank.RejectedError) error {
			locked, err := s.transactions.GetForUpdate(ctx, tx, txn.ID)
			if err != nil {
				return err
			}
			if locked.Status != transaction.StatusPending {
				return &transaction.StatusConflictError{Expected: transaction.StatusPending, Current: locked.Status}
			}
			return s.failAndRefund(ctx, tx, locked, rejected.Code, rejected.Message)
		},
	})
}

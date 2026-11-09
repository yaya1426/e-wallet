package payment

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// EventTopUpRequested asks the outbox to send the debit to the bank (TDR-006).
const EventTopUpRequested = "topup.requested"

// RequestTopUp accepts a top-up (PRD-002 §4): checks 6–9 → transaction (TOPUP, PENDING,
// with the bank account snapshot) → outbox event topup.requested, in one unit of work with
// the idempotency key (see request). No ledger entry is written: the wallet is credited only
// when the bank confirms (TU-BR-1).
func (s *Service) RequestTopUp(ctx context.Context, in RequestInput, render Renderer) (Response, error) {
	return s.request(ctx, in, render, func(ctx context.Context, tx pgx.Tx) (transaction.Transaction, error) {
		w, snapshot, err := s.checkedWallet(ctx, tx, in.UserID) // 6, 7
		if err != nil {
			return transaction.Transaction{}, err
		}
		amount := in.Amount.Amount()
		if err := s.limits.CheckTopUpAmount(amount); err != nil { // 8
			return transaction.Transaction{}, err
		}
		// The wallet account lock serializes this user's top-up checks: two parallel top-ups
		// cannot both pass the maximum-balance check (decided 2026-11-05).
		accounts, err := s.ledger.LockAccounts(ctx, tx, []ledger.AccountID{w.LedgerAccountID})
		if err != nil {
			return transaction.Transaction{}, err
		}
		pending, err := s.transactions.SumPendingTopUps(ctx, tx, w.ID)
		if err != nil {
			return transaction.Transaction{}, err
		}
		if err := s.limits.CheckTopUpBalance(accounts[w.LedgerAccountID].Balance.Amount(), pending, amount); err != nil { // 9
			return transaction.Transaction{}, err
		}

		txn, err := s.transactions.Create(ctx, tx, transaction.New{
			Type: transaction.TypeTopUp, Status: transaction.StatusPending, InitiatorUserID: in.UserID,
			DestinationWalletID: w.ID, Amount: in.Amount, IdempotencyKey: in.Idempotency.Key, BankAccount: snapshot,
		})
		if err != nil {
			return transaction.Transaction{}, err
		}
		return txn, s.enqueueRequested(ctx, tx, EventTopUpRequested, txn.ID)
	})
}

// deliverTopUpRequested asks the bank to pull the money (see deliverRequested). A rejection
// fails the top-up at once with the bank's code and message (TU-BR-5).
func (s *Service) deliverTopUpRequested(ctx context.Context, job queue.Job) queue.Result {
	return s.deliverRequested(ctx, job, bankFlow{
		txnType: transaction.TypeTopUp,
		call: func(ctx context.Context, txn transaction.Transaction) (bank.Acceptance, error) {
			return s.bank.CreateDebit(ctx, bank.DebitRequest{
				IdempotencyKey: txn.ID, Amount: txn.Amount, IBAN: txn.BankAccount.IBAN, HolderName: txn.BankAccount.HolderName,
			})
		},
		reject: func(ctx context.Context, tx pgx.Tx, txn transaction.Transaction, rejected *bank.RejectedError) error {
			code, msg := rejected.Code, rejected.Message
			_, err := s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusFailed,
				transaction.Patch{FailureCode: &code, FailureMessage: &msg})
			return err
		},
	})
}

func decodePayload(raw []byte, v any) error { return json.Unmarshal(raw, v) }

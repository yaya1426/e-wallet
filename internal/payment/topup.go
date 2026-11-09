package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// EventTopUpRequested asks the outbox to send the debit to the bank (TDR-006).
const EventTopUpRequested = "topup.requested"

// ErrBankAccountRequired means the user has no linked bank account (PRD-002 §3).
var ErrBankAccountRequired = errors.New("payment: a linked bank account is required")

// TopUpInput is a validated top-up request.
type TopUpInput struct {
	UserID      uuid.UUID
	Amount      money.Money
	Idempotency idempotency.Request
}

// requestedPayload is the payload of topup.requested (and withdrawal.requested).
type requestedPayload struct {
	TransactionID uuid.UUID `json:"transaction_id"`
}

// topUpBusinessErrors are the errors of checks 6–9: rendered, stored for the key, replayed.
var topUpBusinessErrors = []error{wallet.ErrFrozen, ErrBankAccountRequired, wallet.ErrAmountOutOfRange, wallet.ErrWalletLimitExceeded}

func isOneOf(err error, targets []error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// RequestTopUp accepts a top-up in one unit of work (PRD-002 §4): idempotency Begin →
// checks 6–9 → transaction (TOPUP, PENDING, with the bank account snapshot) → outbox event
// topup.requested → idempotency Complete. No ledger entry is written: the wallet is credited
// only when the bank confirms (TU-BR-1). A business error is rendered, stored with Complete
// and committed, so the same key replays it; nothing else is written then. Other errors roll
// everything back and are returned (idempotency errors, database.ErrTryAgain, failures).
// After the commit the outbox is woken, so the bank call starts at once.
func (s *Service) RequestTopUp(ctx context.Context, in TopUpInput, render Renderer) (Response, error) {
	var resp Response
	err := s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		resp = Response{}
		replay, err := s.idempotency.Begin(ctx, tx, in.Idempotency)
		if err != nil {
			return err
		}
		if replay != nil {
			resp.Replay = replay
			return nil
		}
		txn, reqErr := s.requestTopUp(ctx, tx, in)
		if reqErr != nil && !isOneOf(reqErr, topUpBusinessErrors) {
			return reqErr
		}
		status, body := render(txn, reqErr)
		var txnID *uuid.UUID
		if reqErr == nil {
			txnID = &txn.ID
			resp.TransactionID = txn.ID
		}
		if err := s.idempotency.Complete(ctx, tx, in.UserID, in.Idempotency.Key, status, body, txnID); err != nil {
			return err
		}
		resp.Status, resp.Body = status, body
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if resp.TransactionID != uuid.Nil {
		s.outbox.Wake() // after the commit, never inside it (TDR-006 §2)
	}
	return resp, nil
}

// requestTopUp does checks 6–9 and, if they pass, writes the transaction and the outbox
// event. The checks come before any write, so a business error leaves tx usable for Complete.
func (s *Service) requestTopUp(ctx context.Context, tx pgx.Tx, in TopUpInput) (transaction.Transaction, error) {
	w, err := s.wallets.GetByUserID(ctx, tx, in.UserID)
	if err != nil {
		return transaction.Transaction{}, err
	}
	if w.Status == wallet.StatusFrozen { // 6
		return transaction.Transaction{}, wallet.ErrFrozen
	}
	account, err := s.identity.GetBankAccount(ctx, tx, in.UserID) // 7
	if errors.Is(err, identity.ErrBankAccountNotFound) {
		return transaction.Transaction{}, ErrBankAccountRequired
	}
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
		Type:                transaction.TypeTopUp,
		Status:              transaction.StatusPending,
		InitiatorUserID:     in.UserID,
		DestinationWalletID: w.ID,
		Amount:              in.Amount,
		IdempotencyKey:      in.Idempotency.Key,
		BankAccount:         &transaction.BankAccountSnapshot{IBAN: account.IBAN, HolderName: account.HolderName},
	})
	if err != nil {
		return transaction.Transaction{}, err
	}
	if err := s.outbox.Enqueue(ctx, tx, outbox.Event{
		AggregateType: "transaction", AggregateID: txn.ID, Type: EventTopUpRequested,
		Payload: requestedPayload{TransactionID: txn.ID},
	}); err != nil {
		return transaction.Transaction{}, fmt.Errorf("payment: enqueue %s: %w", EventTopUpRequested, err)
	}
	return txn, nil
}

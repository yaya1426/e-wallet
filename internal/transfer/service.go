package transfer

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// Renderer turns the outcome of a transfer into the HTTP response to send and to store for
// idempotent replays: the result on success, or a business error.
type Renderer func(Result, error) (status int, body any)

// Response is what the handler sends: a fresh response, or a stored one to replay.
type Response struct {
	Status int
	Body   any
	Replay *idempotency.Replay
}

// Service runs P2P transfers.
type Service struct {
	db           *database.DB
	identity     *identity.Service
	wallets      *wallet.Service
	ledger       *ledger.Service
	transactions *transaction.Service
	idempotency  *idempotency.Service
	limits       wallet.Limits
	clock        clock.Clock
}

// NewService returns the transfer service.
func NewService(db *database.DB, identitySvc *identity.Service, wallets *wallet.Service, ledgerSvc *ledger.Service,
	transactions *transaction.Service, idem *idempotency.Service, limits wallet.Limits, clk clock.Clock,
) *Service {
	return &Service{db: db, identity: identitySvc, wallets: wallets, ledger: ledgerSvc,
		transactions: transactions, idempotency: idem, limits: limits, clock: clk}
}

// Send runs one transfer in a single unit of work (PRD-004 §4, TDR-010):
// idempotency Begin → checks 8–14 → transaction (P2P, COMPLETED) → ledger entry
// (Dr sender / Cr recipient) → idempotency Complete. A business error is rendered, stored
// with Complete and committed, so the same key replays it; nothing else is written then.
// Other errors roll everything back (the key included) and are returned: idempotency
// errors, database.ErrTryAgain, or unexpected failures.
func (s *Service) Send(ctx context.Context, in Input, render Renderer) (Response, error) {
	var resp Response
	err := s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		replay, err := s.idempotency.Begin(ctx, tx, in.Idempotency)
		if err != nil {
			return err
		}
		if replay != nil {
			resp = Response{Replay: replay}
			return nil
		}

		result, sendErr := s.send(ctx, tx, in)
		if sendErr != nil && !isBusinessError(sendErr) {
			return sendErr
		}
		status, body := render(result, sendErr)
		var txnID *uuid.UUID
		if sendErr == nil {
			txnID = &result.Transaction.ID
		}
		if err := s.idempotency.Complete(ctx, tx, in.SenderID, in.Idempotency.Key, status, body, txnID); err != nil {
			return err
		}
		resp = Response{Status: status, Body: body}
		return nil
	})
	return resp, err
}

// send does checks 8–14 and, if they pass, writes the transaction and the ledger entry. The
// checks come before any write, so a business error leaves the tx usable for Complete.
func (s *Service) send(ctx context.Context, tx pgx.Tx, in Input) (Result, error) {
	amount := in.Amount.Amount()
	if err := s.limits.CheckP2PAmount(amount); err != nil { // 8
		return Result{}, err
	}
	recipient, err := s.identity.GetUserByPhone(ctx, tx, in.RecipientPhone) // 9
	if errors.Is(err, identity.ErrUserNotFound) {
		return Result{}, ErrRecipientNotFound
	}
	if err != nil {
		return Result{}, err
	}
	if recipient.ID == in.SenderID { // 10
		return Result{}, ErrSelfTransfer
	}
	from, err := s.wallets.GetByUserID(ctx, tx, in.SenderID)
	if err != nil {
		return Result{}, err
	}
	to, err := s.wallets.GetByUserID(ctx, tx, recipient.ID)
	if err != nil {
		return Result{}, err
	}
	if from.Status == wallet.StatusFrozen { // 11 (a frozen recipient may still receive, BR-8)
		return Result{}, wallet.ErrFrozen
	}

	accounts, err := s.ledger.LockAccounts(ctx, tx, []ledger.AccountID{from.LedgerAccountID, to.LedgerAccountID})
	if err != nil {
		return Result{}, err
	}
	if accounts[from.LedgerAccountID].Balance.Amount() < amount { // 12
		return Result{}, wallet.ErrInsufficientFunds
	}
	dayStart, dayEnd := clock.CairoDay(s.clock.Now())
	spent, err := s.transactions.SumOutgoingForDay(ctx, tx, from.ID, dayStart, dayEnd)
	if err != nil {
		return Result{}, err
	}
	if err := s.limits.CheckDailyOutgoing(spent, amount); err != nil { // 13
		return Result{}, err
	}
	if err := s.limits.CheckRecipientBalance(accounts[to.LedgerAccountID].Balance.Amount(), amount); err != nil { // 14
		return Result{}, err
	}

	txn, err := s.transactions.Create(ctx, tx, transaction.New{
		Type:                transaction.TypeP2P,
		Status:              transaction.StatusCompleted,
		InitiatorUserID:     in.SenderID,
		SourceWalletID:      from.ID,
		DestinationWalletID: to.ID,
		Amount:              in.Amount,
		IdempotencyKey:      in.Idempotency.Key,
		Note:                in.Note,
	})
	if err != nil {
		return Result{}, err
	}
	posted, err := s.ledger.Post(ctx, tx, ledger.Entry{
		TransactionID: txn.ID,
		Type:          ledger.EntryP2P,
		Postings: []ledger.Posting{
			{AccountID: from.LedgerAccountID, Direction: ledger.Debit, Amount: in.Amount},
			{AccountID: to.LedgerAccountID, Direction: ledger.Credit, Amount: in.Amount},
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("transfer: post: %w", err)
	}
	return Result{
		Transaction:         txn,
		RecipientPhone:      recipient.Phone,
		RecipientMaskedName: identity.MaskName(recipient.FullName),
		SenderBalanceAfter:  posted.BalancesAfter[from.LedgerAccountID],
	}, nil
}

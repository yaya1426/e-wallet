package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// ErrBankAccountRequired means the user has no linked bank account (PRD-002 §3, PRD-003 §3).
var ErrBankAccountRequired = errors.New("payment: a linked bank account is required")

// RequestInput is a validated top-up or withdrawal request.
type RequestInput struct {
	UserID      uuid.UUID
	Amount      money.Money
	Idempotency idempotency.Request
}

// requestedPayload is the payload of topup.requested and withdrawal.requested.
type requestedPayload struct {
	TransactionID uuid.UUID `json:"transaction_id"`
}

// request runs the unit of work shared by top-up and withdrawal requests: idempotency Begin →
// create (the flow's checks and writes) → idempotency Complete. A business error (see
// businessErrors) is rendered, stored with Complete and committed, so the same key replays
// it; create must return it before writing anything. Other errors roll everything back and
// are returned (idempotency errors, database.ErrTryAgain, failures). After a commit that
// created a transaction, the outbox is woken so the bank call starts at once.
func (s *Service) request(ctx context.Context, in RequestInput, render Renderer,
	create func(ctx context.Context, tx pgx.Tx) (transaction.Transaction, error),
) (Response, error) {
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
		txn, reqErr := create(ctx, tx)
		if reqErr != nil && !isBusinessError(reqErr) {
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

// checkedWallet does the checks shared by both flows: the user's wallet is not frozen (6)
// and has a linked bank account (7), whose snapshot it returns.
func (s *Service) checkedWallet(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (wallet.Wallet, *transaction.BankAccountSnapshot, error) {
	w, err := s.wallets.GetByUserID(ctx, tx, userID)
	if err != nil {
		return wallet.Wallet{}, nil, err
	}
	if w.Status == wallet.StatusFrozen { // 6
		return wallet.Wallet{}, nil, wallet.ErrFrozen
	}
	account, err := s.identity.GetBankAccount(ctx, tx, userID) // 7
	if errors.Is(err, identity.ErrBankAccountNotFound) {
		return wallet.Wallet{}, nil, ErrBankAccountRequired
	}
	if err != nil {
		return wallet.Wallet{}, nil, err
	}
	return w, &transaction.BankAccountSnapshot{IBAN: account.IBAN, HolderName: account.HolderName}, nil
}

// enqueueRequested stores the outbox event that asks the bank, in the request's tx.
func (s *Service) enqueueRequested(ctx context.Context, tx pgx.Tx, eventType string, txnID uuid.UUID) error {
	if err := s.outbox.Enqueue(ctx, tx, outbox.Event{
		AggregateType: "transaction", AggregateID: txnID, Type: eventType,
		Payload: requestedPayload{TransactionID: txnID},
	}); err != nil {
		return fmt.Errorf("payment: enqueue %s: %w", eventType, err)
	}
	return nil
}

// bankFlow is what differs between the top-up and withdrawal outbox handlers.
type bankFlow struct {
	txnType transaction.Type
	// call asks the bank; the idempotency key is the transaction ID (TDR-005 layer 3).
	call func(ctx context.Context, txn transaction.Transaction) (bank.Acceptance, error)
	// reject applies a business rejection to the still-PENDING transaction inside tx.
	reject func(ctx context.Context, tx pgx.Tx, txn transaction.Transaction, rejected *bank.RejectedError) error
}

// deliverRequested is the outbox handler of a request event (TDR-006 §3): it asks the bank
// and records its answer. No DB transaction is open during the bank call (TDR-010 §6).
//   - The transaction is no longer PENDING (e.g. the webhook came first) → Done, no call.
//   - Accepted → PENDING → PROCESSING with the bank reference → Done.
//   - Rejected → the flow's reject (FAILED, plus the refund for a withdrawal) → Done.
//   - Temporary failure (or a DB error) → Retry. Nothing is failed or refunded on a timeout
//     (WD-BR-4): after the last attempt the event is DEAD and the transaction stays PENDING
//     for reconciliation (phase 2).
//
// When the transaction already moved on by the time the answer is recorded, nothing changes
// and the event is Done. A frozen wallet does not stop an accepted request (TU-BR-8, WD-BR-8).
func (s *Service) deliverRequested(ctx context.Context, job queue.Job, flow bankFlow) queue.Result {
	var p requestedPayload
	if err := decodePayload(job.Payload, &p); err != nil {
		return queue.Fail("undecodable payload: " + err.Error())
	}
	txn, err := s.transactions.Get(ctx, s.db.Pool(), p.TransactionID)
	if errors.Is(err, transaction.ErrNotFound) {
		return queue.Fail("transaction not found: " + p.TransactionID.String())
	}
	if err != nil {
		return queue.Retry(err)
	}
	log := s.log.With(slog.String("transaction_id", txn.ID.String()), slog.String("type", string(txn.Type)),
		slog.Int("attempt", job.Attempts))
	if txn.Status != transaction.StatusPending {
		log.InfoContext(ctx, "request already moved on; no bank call", slog.String("status", string(txn.Status)))
		return queue.Done()
	}
	if txn.Type != flow.txnType || txn.BankAccount == nil {
		return queue.Fail(fmt.Sprintf("transaction %s is not a %s with a bank account snapshot", txn.ID, flow.txnType))
	}

	acceptance, err := flow.call(ctx, txn)
	var rejected *bank.RejectedError
	switch {
	case err == nil:
		ref := acceptance.BankReference
		err = s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing,
				transaction.Patch{BankReference: &ref})
			return err
		})
	case errors.As(err, &rejected):
		err = s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return flow.reject(ctx, tx, txn, rejected) })
	default:
		log.WarnContext(ctx, "bank call failed; will retry", slog.Any("error", err))
		return queue.Retry(err)
	}
	switch {
	case err == nil:
		log.InfoContext(ctx, "bank answered the request", slog.Bool("rejected", rejected != nil))
		return queue.Done()
	case errors.Is(err, transaction.ErrStatusConflict):
		log.InfoContext(ctx, "transaction already moved on; bank answer not applied", slog.Any("conflict", err))
		return queue.Done()
	default:
		return queue.Retry(err)
	}
}

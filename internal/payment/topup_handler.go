package payment

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// deliverTopUpRequested is the outbox handler of topup.requested: it asks the bank to pull
// the money (TDR-006 §3, PRD-002 §4). No DB transaction is open during the bank call
// (TDR-010 §6). The idempotency key is the transaction ID (TDR-005 layer 3), so retries never
// create a second debit.
//   - The transaction is no longer PENDING (e.g. the webhook came first, T016) → Done.
//   - Accepted → PENDING → PROCESSING with the bank reference → Done.
//   - Rejected → PENDING → FAILED with the bank's code and message → Done (TU-BR-5).
//   - Temporary failure (or a DB error) → Retry; after the last attempt the event is DEAD and
//     the transaction stays PENDING for reconciliation (phase 2, TU-BR-7).
//
// A transition that finds the transaction already moved on is not an error: Done. A frozen
// wallet does not stop an accepted top-up (TU-BR-8).
func (s *Service) deliverTopUpRequested(ctx context.Context, job queue.Job) queue.Result {
	var p requestedPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return queue.Fail("undecodable payload: " + err.Error())
	}
	txn, err := s.transactions.Get(ctx, s.db.Pool(), p.TransactionID)
	if errors.Is(err, transaction.ErrNotFound) {
		return queue.Fail("transaction not found: " + p.TransactionID.String())
	}
	if err != nil {
		return queue.Retry(err)
	}
	log := s.log.With(slog.String("transaction_id", txn.ID.String()), slog.Int("attempt", job.Attempts))
	if txn.Status != transaction.StatusPending {
		log.InfoContext(ctx, "top-up already moved on; no bank call", slog.String("status", string(txn.Status)))
		return queue.Done()
	}
	if txn.Type != transaction.TypeTopUp || txn.BankAccount == nil {
		return queue.Fail("not a top-up with a bank account snapshot: " + txn.ID.String())
	}

	acceptance, err := s.bank.CreateDebit(ctx, bank.DebitRequest{
		IdempotencyKey: txn.ID, Amount: txn.Amount,
		IBAN: txn.BankAccount.IBAN, HolderName: txn.BankAccount.HolderName,
	})
	var rejected *bank.RejectedError
	switch {
	case err == nil:
		ref := acceptance.BankReference
		return s.finishRequest(ctx, log, txn, transaction.StatusProcessing, transaction.Patch{BankReference: &ref})
	case errors.As(err, &rejected):
		code, msg := rejected.Code, rejected.Message
		return s.finishRequest(ctx, log, txn, transaction.StatusFailed, transaction.Patch{FailureCode: &code, FailureMessage: &msg})
	default:
		log.WarnContext(ctx, "bank debit failed; will retry", slog.Any("error", err))
		return queue.Retry(err)
	}
}

// finishRequest moves a PENDING transaction to `to` after the bank's answer. If it already
// moved on (the webhook or another delivery got there first) nothing changes and the event
// is done.
func (s *Service) finishRequest(ctx context.Context, log *slog.Logger, txn transaction.Transaction,
	to transaction.Status, patch transaction.Patch,
) queue.Result {
	err := s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, to, patch)
		return err
	})
	switch {
	case err == nil:
		log.InfoContext(ctx, "bank answered the request", slog.String("status", string(to)))
		return queue.Done()
	case errors.Is(err, transaction.ErrStatusConflict):
		log.InfoContext(ctx, "transaction already moved on; bank answer not applied", slog.Any("conflict", err))
		return queue.Done()
	default:
		return queue.Retry(err)
	}
}

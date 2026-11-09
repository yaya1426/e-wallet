package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

var _ webhook.EventHandler = (*Service)(nil)

// HandleBankEvent applies a bank webhook event (webhook.EventHandler, TDR-007 §3). It finds
// the transaction by the bank reference or, when the reference is not stored yet (the
// webhook beat the outbox worker), by the idempotency key, which is our transaction ID. No
// match returns webhook.ErrUnknownTransaction, so the inbox retries the event.
//
// Payout events return ErrUnsupportedOutcome until withdrawals are settled (T019), so the
// inbox retries them.
func (s *Service) HandleBankEvent(ctx context.Context, tx pgx.Tx, ev webhook.BankEvent) error {
	outcome := outcomeOf(ev)
	if outcome.Kind == bank.KindPayout {
		return fmt.Errorf("%w: %s event %s", ErrUnsupportedOutcome, ev.Type, ev.EventID)
	}
	txn, err := s.transactions.FindByBankRef(ctx, tx, ev.BankReference)
	switch {
	case err == nil:
		if txn.ID != ev.IdempotencyKey {
			// The bank links this reference to another of our transactions: never guess.
			return s.record(ctx, tx, txn, discrepancy.KindStatusMismatch, discrepancy.SeverityCritical, outcome, map[string]any{
				"reason":                  "the event's idempotency key is not the transaction that has this bank reference",
				"event_id":                ev.EventID,
				"event_idempotency_key":   ev.IdempotencyKey,
				"transaction_with_ref_id": txn.ID,
			})
		}
	case errors.Is(err, transaction.ErrNotFound):
		txn, err = s.transactions.Get(ctx, tx, ev.IdempotencyKey)
		if errors.Is(err, transaction.ErrNotFound) {
			return fmt.Errorf("%w: bank reference %q, idempotency key %s", webhook.ErrUnknownTransaction, ev.BankReference, ev.IdempotencyKey)
		}
		if err != nil {
			return err
		}
	default:
		return err
	}
	return s.ApplyBankOutcome(ctx, tx, txn.ID, outcome)
}

// outcomeOf converts a webhook event (validated by the webhook module) into an Outcome.
func outcomeOf(ev webhook.BankEvent) bank.Outcome {
	kind := bank.KindPayout
	if strings.HasPrefix(ev.Type, "debit.") {
		kind = bank.KindDebit
	}
	status := bank.StatusFailed
	if ev.Succeeded() {
		status = bank.StatusSucceeded
	}
	return bank.Outcome{
		Kind: kind, Status: status, BankReference: ev.BankReference, Amount: ev.Amount,
		FailureCode: ev.FailureCode, FailureMessage: ev.FailureMessage,
	}
}

// ApplyBankOutcome applies the bank's final result to the transaction, inside tx (TDR-007
// §3; the same function serves reconciliation, TDR-009). It locks the transaction row first.
// It is idempotent: an outcome that agrees with a terminal transaction is a no-op. Anything
// that does not add up is recorded as a discrepancy, with no change to money or status, and
// nil is returned (an operator takes over; never auto-fix, TDR-009 §3):
//   - the outcome's kind does not match the transaction type, or the bank references differ;
//   - a terminal transaction and an outcome that contradicts it (PRD-001 BR-15);
//   - the amount (or currency) differs (PRD-002 TU-BR-3).
//
// Otherwise a PENDING transaction first moves to PROCESSING with the outcome's reference;
// then a debit success posts TOPUP_SETTLE and completes the top-up, and a debit failure fails
// it with the bank's code and message. Payout outcomes return ErrUnsupportedOutcome (T019).
func (s *Service) ApplyBankOutcome(ctx context.Context, tx pgx.Tx, txnID uuid.UUID, outcome bank.Outcome) error {
	if !outcome.Final() {
		return fmt.Errorf("%w: status %q", ErrNotFinal, outcome.Status)
	}
	txn, err := s.transactions.GetForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	expected := map[bank.Kind]transaction.Type{bank.KindDebit: transaction.TypeTopUp, bank.KindPayout: transaction.TypeWithdrawal}
	if want, ok := expected[outcome.Kind]; !ok || txn.Type != want {
		return s.record(ctx, tx, txn, discrepancy.KindStatusMismatch, discrepancy.SeverityCritical, outcome, map[string]any{
			"reason": "the outcome's kind does not match the transaction type",
		})
	}
	if outcome.Kind == bank.KindPayout {
		return fmt.Errorf("%w: payout for transaction %s", ErrUnsupportedOutcome, txn.ID)
	}
	if txn.BankReference != "" && outcome.BankReference != "" && txn.BankReference != outcome.BankReference {
		return s.record(ctx, tx, txn, discrepancy.KindStatusMismatch, discrepancy.SeverityCritical, outcome, map[string]any{
			"reason": "the bank reference differs from the stored one",
		})
	}
	if txn.Status.Terminal() {
		if agrees(txn.Status, outcome.Status) {
			s.log.InfoContext(ctx, "bank outcome already applied", attrs(txn, outcome)...)
			return nil
		}
		return s.record(ctx, tx, txn, discrepancy.KindStatusMismatch, discrepancy.SeverityCritical, outcome, map[string]any{
			"reason": "the outcome contradicts the transaction's final status",
		})
	}
	if outcome.Amount != txn.Amount {
		return s.record(ctx, tx, txn, discrepancy.KindAmountMismatch, discrepancy.SeverityCritical, outcome, map[string]any{
			"reason": "the bank's amount differs from the transaction's",
		})
	}
	if txn.Status == transaction.StatusPending {
		var patch transaction.Patch
		if ref := outcome.BankReference; ref != "" { // never store an empty reference
			patch.BankReference = &ref
		}
		if txn, err = s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing, patch); err != nil {
			return err
		}
	}
	if outcome.Status == bank.StatusSucceeded {
		err = s.completeTopUp(ctx, tx, txn)
	} else {
		code, msg := outcome.FailureCode, outcome.FailureMessage
		_, err = s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusProcessing, transaction.StatusFailed,
			transaction.Patch{FailureCode: &code, FailureMessage: &msg})
	}
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "bank outcome applied", attrs(txn, outcome)...)
	return nil
}

// completeTopUp credits the destination wallet (Dr BANK_SETTLEMENT / Cr USER_WALLET, TDR-004
// §3) and completes the transaction. The wallet is credited even when FROZEN (PRD-001 BR-8)
// or above the maximum balance (PRD-002 TU-BR-4): the money is already ours, so there is no
// balance check and no account lock beyond Post's own update.
func (s *Service) completeTopUp(ctx context.Context, tx pgx.Tx, txn transaction.Transaction) error {
	w, err := s.wallets.GetByID(ctx, tx, txn.DestinationWalletID)
	if err != nil {
		return err
	}
	settlement, err := s.ledger.GetAccountByCode(ctx, tx, ledger.CodeBankSettlement)
	if err != nil {
		return err
	}
	if _, err := s.ledger.Post(ctx, tx, ledger.Entry{
		TransactionID: txn.ID,
		Type:          ledger.EntryTopUpSettle,
		Description:   "Top-up settled by the bank",
		Postings: []ledger.Posting{
			{AccountID: settlement.ID, Direction: ledger.Debit, Amount: txn.Amount},
			{AccountID: w.LedgerAccountID, Direction: ledger.Credit, Amount: txn.Amount},
		},
	}); err != nil {
		// ErrDuplicateEntry cannot happen after the status checks above; if it does, it is a
		// bug to look at, so it is returned (the event is retried and logged), never ignored.
		return err
	}
	_, err = s.transactions.Transition(ctx, tx, txn.ID, transaction.StatusProcessing, transaction.StatusCompleted, transaction.Patch{})
	return err
}

// agrees reports whether a terminal status is the one the outcome leads to.
func agrees(status transaction.Status, outcome bank.OperationStatus) bool {
	return (status == transaction.StatusCompleted && outcome == bank.StatusSucceeded) ||
		(status == transaction.StatusFailed && outcome == bank.StatusFailed)
}

// record stores a discrepancy about txn in tx and returns nil: the event is handled, an
// operator takes over. details gets the transaction's and the outcome's facts added.
func (s *Service) record(ctx context.Context, tx pgx.Tx, txn transaction.Transaction, kind discrepancy.Kind,
	severity discrepancy.Severity, outcome bank.Outcome, details map[string]any,
) error {
	details["transaction_type"] = txn.Type
	details["transaction_status"] = txn.Status
	details["transaction_amount_minor"] = txn.Amount.Amount()
	details["transaction_bank_reference"] = txn.BankReference
	details["outcome_kind"] = outcome.Kind
	details["outcome_status"] = outcome.Status
	details["outcome_amount_minor"] = outcome.Amount.Amount()
	details["outcome_currency"] = outcome.Amount.Currency()
	details["outcome_bank_reference"] = outcome.BankReference
	if err := s.discrepancies.Record(ctx, tx, discrepancy.New{
		Kind: kind, Severity: severity, TransactionID: txn.ID, Details: details,
	}); err != nil {
		return err
	}
	s.log.WarnContext(ctx, "bank outcome not applied", append(attrs(txn, outcome), slog.String("discrepancy", string(kind)))...)
	return nil
}

func attrs(txn transaction.Transaction, outcome bank.Outcome) []any {
	return []any{
		slog.String("transaction_id", txn.ID.String()), slog.String("transaction_status", string(txn.Status)),
		slog.String("outcome_kind", string(outcome.Kind)), slog.String("outcome_status", string(outcome.Status)),
		slog.String("bank_reference", outcome.BankReference),
	}
}

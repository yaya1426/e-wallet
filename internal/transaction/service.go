package transaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/transaction/internal/sqlcgen"
)

// Service creates transactions and moves them through the state machine. Writes take the
// caller's pgx.Tx so they commit with the ledger entry and outbox event (TDR-001 §3).
type Service struct {
	clock clock.Clock
}

// NewService returns a transaction service.
func NewService(clk clock.Clock) *Service {
	return &Service{clock: clk}
}

// Create inserts a transaction. A P2P transaction is created COMPLETED (completed_at set),
// top-ups and withdrawals PENDING. The same user reusing an idempotency key returns
// ErrDuplicateIdempotencyKey (TDR-005 layer 2); the DB transaction is then aborted.
func (s *Service) Create(ctx context.Context, tx pgx.Tx, n New) (Transaction, error) {
	if err := n.validate(); err != nil {
		return Transaction{}, err
	}
	var snapshot []byte
	if n.BankAccount != nil {
		var err error
		if snapshot, err = json.Marshal(n.BankAccount); err != nil {
			return Transaction{}, fmt.Errorf("transaction: encode bank account snapshot: %w", err)
		}
	}
	now := s.now()
	var completedAt *time.Time
	if n.Status.Terminal() {
		completedAt = &now
	}
	row, err := queries(tx).CreateTransaction(ctx, sqlcgen.CreateTransactionParams{
		ID:                  id.New(),
		Type:                string(n.Type),
		Status:              string(n.Status),
		InitiatorUserID:     n.InitiatorUserID,
		SourceWalletID:      nullUUID(n.SourceWalletID),
		DestinationWalletID: nullUUID(n.DestinationWalletID),
		Amount:              n.Amount.Amount(),
		Currency:            string(n.Amount.Currency()),
		IdempotencyKey:      n.IdempotencyKey,
		BankAccountSnapshot: snapshot,
		Note:                optional(n.Note),
		Now:                 now,
		CompletedAt:         completedAt,
	})
	if violates(err, codeUniqueViolation, constraintIdempotencyKey) {
		return Transaction{}, ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction: create: %w", err)
	}
	return toTransaction(row)
}

// Get returns the transaction. q is a pgx.Tx or the pool.
func (s *Service) Get(ctx context.Context, q Querier, txnID uuid.UUID) (Transaction, error) {
	return one(queries(q).GetTransaction(ctx, txnID))
}

// GetForUpdate returns the transaction and locks its row until tx ends.
func (s *Service) GetForUpdate(ctx context.Context, tx pgx.Tx, txnID uuid.UUID) (Transaction, error) {
	return one(queries(tx).GetTransactionForUpdate(ctx, txnID))
}

// FindByBankRef returns the transaction with the bank's reference.
func (s *Service) FindByBankRef(ctx context.Context, q Querier, bankReference string) (Transaction, error) {
	return one(queries(q).GetTransactionByBankReference(ctx, &bankReference))
}

// Transition moves the transaction from → to and applies patch, only if the row is still in
// from (architecture §5.5). An invalid pair returns ErrInvalidTransition without SQL; a row
// in another status returns *StatusConflictError (ErrStatusConflict); an unknown ID returns
// ErrNotFound; a bank reference used by another transaction returns ErrDuplicateBankReference
// (the DB transaction is then aborted). Terminal statuses set completed_at.
func (s *Service) Transition(ctx context.Context, tx pgx.Tx, txnID uuid.UUID, from, to Status, patch Patch) (Transaction, error) {
	if !CanTransition(from, to) {
		return Transaction{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	row, err := queries(tx).TransitionTransaction(ctx, sqlcgen.TransitionTransactionParams{
		ToStatus:       string(to),
		BankReference:  patch.BankReference,
		FailureCode:    patch.FailureCode,
		FailureMessage: patch.FailureMessage,
		Now:            s.now(),
		ID:             txnID,
		FromStatus:     string(from),
	})
	switch {
	case violates(err, codeUniqueViolation, constraintBankReference):
		return Transaction{}, ErrDuplicateBankReference
	case errors.Is(err, pgx.ErrNoRows):
		current, getErr := s.Get(ctx, tx, txnID)
		if getErr != nil {
			return Transaction{}, getErr
		}
		return Transaction{}, &StatusConflictError{Expected: from, Current: current.Status}
	case err != nil:
		return Transaction{}, fmt.Errorf("transaction: transition %s: %w", txnID, err)
	}
	return toTransaction(row)
}

// SumOutgoingForDay returns, in piasters, what the wallet sent in [dayStart, dayEnd):
// withdrawals that are not FAILED plus completed P2P (PRD-001 §5.6, PRD-003 WD-BR-7). Use
// clock.CairoDay for the bounds, and lock the wallet's ledger account first so the sum
// cannot change under the caller.
func (s *Service) SumOutgoingForDay(ctx context.Context, q Querier, walletID uuid.UUID, dayStart, dayEnd time.Time) (int64, error) {
	sum, err := queries(q).SumOutgoingForDay(ctx, sqlcgen.SumOutgoingForDayParams{
		WalletID: nullUUID(walletID), DayStart: dayStart, DayEnd: dayEnd,
	})
	if err != nil {
		return 0, fmt.Errorf("transaction: sum outgoing: %w", err)
	}
	return sum, nil
}

// SumPendingTopUps returns, in piasters, the top-ups to the wallet still PENDING or
// PROCESSING (PRD-002 §3.5).
func (s *Service) SumPendingTopUps(ctx context.Context, q Querier, walletID uuid.UUID) (int64, error) {
	sum, err := queries(q).SumPendingTopUps(ctx, nullUUID(walletID))
	if err != nil {
		return 0, fmt.Errorf("transaction: sum pending top-ups: %w", err)
	}
	return sum, nil
}

// now is the clock time truncated to PostgreSQL's microsecond precision, so returned
// values equal later reads.
func (s *Service) now() time.Time { return s.clock.Now().Truncate(time.Microsecond) }

func one(row sqlcgen.Transaction, err error) (Transaction, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction: get: %w", err)
	}
	return toTransaction(row)
}

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

// Page is one page of a wallet's history, newest first. Next is nil on the last page.
type Page struct {
	Items []Transaction
	Next  *Cursor
}

// ListForWallet returns up to limit transactions where the wallet is the source or the
// destination, newest first, starting after after (nil for the first page).
func (s *Service) ListForWallet(ctx context.Context, q Querier, walletID uuid.UUID, after *Cursor, limit int) (Page, error) {
	params := sqlcgen.ListForWalletParams{RowLimit: int32(limit + 1), WalletID: nullUUID(walletID)} //nolint:gosec // limit ≤ 100
	if after != nil {
		params.HasCursor, params.CursorTime, params.CursorID = true, after.CreatedAt, after.ID
	}
	rows, err := queries(q).ListForWallet(ctx, params)
	if err != nil {
		return Page{}, fmt.Errorf("transaction: list for wallet: %w", err)
	}
	page := Page{Items: make([]Transaction, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit { // the extra row only tells that there is a next page
			last := page.Items[limit-1]
			page.Next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
			break
		}
		txn, err := toTransaction(row)
		if err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, txn)
	}
	return page, nil
}

// GetVisible returns the transaction if the wallet is its source or destination, otherwise
// ErrNotFound (PRD-001 US-5).
func (s *Service) GetVisible(ctx context.Context, q Querier, txnID, walletID uuid.UUID) (Transaction, error) {
	return one(queries(q).GetVisibleTransaction(ctx, sqlcgen.GetVisibleTransactionParams{ID: txnID, WalletID: nullUUID(walletID)}))
}

// ListForReconciliation returns up to limit top-ups and withdrawals still PROCESSING that were
// created before createdBefore and not reconciled since checkedBefore (never reconciled
// counts as due), oldest first (TDR-009 §2).
func (s *Service) ListForReconciliation(ctx context.Context, q Querier, createdBefore, checkedBefore time.Time, limit int) ([]Transaction, error) {
	rows, err := queries(q).ListForReconciliation(ctx, sqlcgen.ListForReconciliationParams{
		CreatedBefore: createdBefore, CheckedBefore: checkedBefore, RowLimit: int32(max(0, min(limit, 1<<31-1))),
	})
	if err != nil {
		return nil, fmt.Errorf("transaction: list for reconciliation: %w", err)
	}
	out := make([]Transaction, 0, len(rows))
	for _, row := range rows {
		txn, err := toTransaction(row)
		if err != nil {
			return nil, err
		}
		out = append(out, txn)
	}
	return out, nil
}

// ListStuck returns up to limit top-ups and withdrawals still PENDING or PROCESSING that were
// created before createdBefore, oldest first (TDR-009 stuck_alert).
func (s *Service) ListStuck(ctx context.Context, q Querier, createdBefore time.Time, limit int) ([]Transaction, error) {
	rows, err := queries(q).ListStuck(ctx, sqlcgen.ListStuckParams{CreatedBefore: createdBefore, RowLimit: int32(max(0, min(limit, 1<<31-1)))})
	if err != nil {
		return nil, fmt.Errorf("transaction: list stuck: %w", err)
	}
	out := make([]Transaction, 0, len(rows))
	for _, row := range rows {
		txn, err := toTransaction(row)
		if err != nil {
			return nil, err
		}
		out = append(out, txn)
	}
	return out, nil
}

// CountStuck counts, per type (TOPUP, WITHDRAWAL), the transactions ListStuck would return
// without a limit. Types with none are present with 0.
func (s *Service) CountStuck(ctx context.Context, q Querier, createdBefore time.Time) (map[Type]int64, error) {
	rows, err := queries(q).CountStuck(ctx, createdBefore)
	if err != nil {
		return nil, fmt.Errorf("transaction: count stuck: %w", err)
	}
	out := map[Type]int64{TypeTopUp: 0, TypeWithdrawal: 0}
	for _, r := range rows {
		out[Type(r.Type)] = r.Stuck
	}
	return out, nil
}

// MarkReconciled records that reconciliation checked the transaction at `at`. It changes
// neither the status nor updated_at: it is bookkeeping, not a business change.
func (s *Service) MarkReconciled(ctx context.Context, q Querier, txnID uuid.UUID, at time.Time) error {
	n, err := queries(q).MarkReconciled(ctx, sqlcgen.MarkReconciledParams{At: at.Truncate(time.Microsecond), ID: txnID})
	if err != nil {
		return fmt.Errorf("transaction: mark reconciled: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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

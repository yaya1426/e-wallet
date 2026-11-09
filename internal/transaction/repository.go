package transaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction/internal/sqlcgen"
)

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool for plain reads.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Constraint names from migrations/000005_transactions.up.sql that map to domain errors.
const (
	constraintIdempotencyKey = "transactions_initiator_idempotency_key"
	constraintBankReference  = "transactions_bank_reference_key"
	codeUniqueViolation      = "23505"
)

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

func violates(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func nullUUID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toTransaction(row sqlcgen.Transaction) (Transaction, error) {
	amount, err := money.NewPositive(row.Amount, money.Currency(row.Currency))
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction %s: %w", row.ID, err)
	}
	var snapshot *BankAccountSnapshot
	if len(row.BankAccountSnapshot) > 0 {
		snapshot = &BankAccountSnapshot{}
		if err := json.Unmarshal(row.BankAccountSnapshot, snapshot); err != nil {
			return Transaction{}, fmt.Errorf("transaction %s: bank account snapshot: %w", row.ID, err)
		}
	}
	return Transaction{
		ID:                  row.ID,
		Type:                Type(row.Type),
		Status:              Status(row.Status),
		InitiatorUserID:     row.InitiatorUserID,
		SourceWalletID:      row.SourceWalletID.UUID,
		DestinationWalletID: row.DestinationWalletID.UUID,
		Amount:              amount,
		IdempotencyKey:      row.IdempotencyKey,
		BankReference:       value(row.BankReference),
		BankAccount:         snapshot,
		Note:                value(row.Note),
		FailureCode:         value(row.FailureCode),
		FailureMessage:      value(row.FailureMessage),
		LastReconciledAt:    row.LastReconciledAt,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		CompletedAt:         row.CompletedAt,
	}, nil
}

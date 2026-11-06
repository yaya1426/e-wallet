package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/ledger/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool for plain reads.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Constraint names from migrations/000002_ledger.up.sql that map to domain errors.
const (
	constraintUserWalletNonNegative = "ledger_accounts_user_wallet_non_negative"
	constraintEntryPerTransaction   = "journal_entries_transaction_entry_type_key"
	constraintAccountCode           = "ledger_accounts_code_key"
)

// SQLSTATE codes.
const (
	codeUniqueViolation = "23505"
	codeCheckViolation  = "23514"
)

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

// violates reports whether err is the given SQLSTATE on the named constraint.
func violates(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func toAccount(row sqlcgen.LedgerAccount) (Account, error) {
	balance, err := money.New(row.Balance, money.Currency(row.Currency))
	if err != nil {
		return Account{}, fmt.Errorf("ledger: account %s: %w", row.ID, err)
	}
	return Account{
		ID:         AccountID(row.ID),
		Code:       row.Code,
		Kind:       Kind(row.Kind),
		Type:       AccountType(row.Type),
		NormalSide: Direction(row.NormalSide),
		Balance:    balance,
		Version:    row.Version,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}

func uuids(ids []AccountID) []uuid.UUID {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		out[i] = uuid.UUID(id)
	}
	return out
}

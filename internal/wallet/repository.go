package wallet

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/wallet/internal/sqlcgen"
)

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool for plain reads.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

func toWallet(row sqlcgen.Wallet) Wallet {
	return Wallet{
		ID:              row.ID,
		UserID:          row.UserID,
		LedgerAccountID: ledger.AccountID(row.LedgerAccountID),
		Currency:        money.Currency(row.Currency),
		Status:          Status(row.Status),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

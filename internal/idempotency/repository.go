package idempotency

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/idempotency/internal/sqlcgen"
)

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool for plain reads.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

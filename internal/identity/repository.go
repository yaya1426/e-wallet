package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/identity/internal/sqlcgen"
)

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool for plain reads.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// constraintPhoneUnique is the unique constraint on users.phone_e164 (migration 000003).
const constraintPhoneUnique = "users_phone_e164_key"

const codeUniqueViolation = "23505"

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

// violates reports whether err is the given SQLSTATE on the named constraint.
func violates(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func toUser(row sqlcgen.User) User {
	return User{
		ID:        row.ID,
		Phone:     row.PhoneE164,
		FullName:  row.FullName,
		Status:    Status(row.Status),
		CreatedAt: row.CreatedAt,
	}
}

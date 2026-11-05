package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrTryAgain means a transaction kept conflicting with others (deadlock, serialization
// failure, lock timeout) and the retries ran out. The HTTP layer answers 503 TRY_AGAIN.
var ErrTryAgain = errors.New("database: transaction conflict, try again")

// Retry reasons, by SQLSTATE (TDR-010 §5).
const (
	reasonSerialization = "serialization_failure" // 40001
	reasonDeadlock      = "deadlock"              // 40P01
	reasonLockTimeout   = "lock_timeout"          // 55P03
)

// Wait between attempts: a random duration in [minBackoff, maxBackoff].
const (
	minBackoff = 10 * time.Millisecond
	maxBackoff = 50 * time.Millisecond
)

// InTx runs fn in one READ COMMITTED transaction with lock_timeout and statement_timeout
// set for that transaction only, and commits when fn returns nil (TDR-010).
//
// When fn or the commit fails with a serialization failure, a deadlock or a lock timeout,
// the whole transaction is retried up to MaxRetries times after a short random wait; when
// the retries run out the error matches both ErrTryAgain and the last *pgconn.PgError.
// Any other error, including fn's own errors, is returned unchanged after a rollback.
//
// fn may run more than once, so it must have no side effects outside tx (no network
// calls, TDR-010 §6). InTx must not be nested, and tx must not be used after fn returns.
// A panic in fn rolls the transaction back and is re-raised.
func (db *DB) InTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	attempts := db.tx.MaxRetries + 1
	for attempt := 1; ; attempt++ {
		err := db.runOnce(ctx, fn)
		if err == nil {
			return nil
		}
		reason, retryable := retryReason(err)
		if !retryable {
			return err
		}
		if attempt >= attempts {
			db.observer.ObserveTxTryAgain()
			db.log.WarnContext(ctx, "transaction retries exhausted",
				slog.String("reason", reason), slog.Int("attempts", attempt), slog.Any("error", err))
			return fmt.Errorf("%w: %w", ErrTryAgain, err)
		}
		db.observer.ObserveTxRetry(reason)
		if err := sleep(ctx, db.backoff()); err != nil {
			return err
		}
	}
}

// runOnce runs one attempt: begin, set the timeouts, fn, commit.
func (db *DB) runOnce(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: begin: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// Roll back even when ctx is already canceled.
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			db.log.WarnContext(ctx, "transaction rollback failed", slog.Any("error", rbErr))
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true), set_config('statement_timeout', $2, true)`,
		milliseconds(db.tx.LockTimeout), milliseconds(db.tx.StatementTimeout)); err != nil {
		return fmt.Errorf("database: set transaction timeouts: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	committed = true
	return nil
}

// retryReason reports whether err is a transient conflict worth retrying, and which one.
func retryReason(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	switch pgErr.Code {
	case "40001":
		return reasonSerialization, true
	case "40P01":
		return reasonDeadlock, true
	case "55P03":
		return reasonLockTimeout, true
	default:
		return "", false
	}
}

func retryBackoff() time.Duration {
	return minBackoff + rand.N(maxBackoff-minBackoff+1) //nolint:gosec // jitter, not security
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func milliseconds(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}

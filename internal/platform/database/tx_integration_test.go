//go:build integration

package database_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
)

// fakeObserver records InTx retry events.
type fakeObserver struct {
	mu       sync.Mutex
	retries  map[string]int
	tryAgain int
}

func (o *fakeObserver) ObserveTxRetry(reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.retries == nil {
		o.retries = map[string]int{}
	}
	o.retries[reason]++
}

func (o *fakeObserver) ObserveTxTryAgain() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tryAgain++
}

func (o *fakeObserver) counts() (map[string]int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	copied := map[string]int{}
	for k, v := range o.retries {
		copied[k] = v
	}
	return copied, o.tryAgain
}

// syncBuffer is a log sink that is safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testDB struct {
	*database.DB
	observer *fakeObserver
	logs     *syncBuffer
}

// openTestDB opens a fresh database with table t(id int primary key, v int) holding rows 1 and 2.
func openTestDB(t *testing.T, settings database.TxSettings) testDB {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	obs := &fakeObserver{}
	logs := &syncBuffer{}
	db, err := database.Open(ctx, url, database.Options{
		Tx:       settings,
		Logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		Observer: obs,
	})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Pool().Exec(ctx, `CREATE TABLE t (id int PRIMARY KEY, v int NOT NULL); INSERT INTO t VALUES (1, 0), (2, 0)`)
	require.NoError(t, err)
	return testDB{DB: db, observer: obs, logs: logs}
}

func value(t *testing.T, db testDB, id int) int {
	t.Helper()
	var v int
	require.NoError(t, db.Pool().QueryRow(context.Background(), `SELECT v FROM t WHERE id = $1`, id).Scan(&v))
	return v
}

var errBusiness = errors.New("business rule failed")

func TestInTx_Commit(t *testing.T) {
	db := openTestDB(t, database.DefaultTxSettings())

	err := db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE t SET v = 7 WHERE id = 1`)
		return err
	})

	require.NoError(t, err)
	assert.Equal(t, 7, value(t, db, 1))
}

func TestInTx_RollbackOnError(t *testing.T) {
	db := openTestDB(t, database.DefaultTxSettings())
	calls := 0

	err := db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		calls++
		if _, err := tx.Exec(ctx, `UPDATE t SET v = 7 WHERE id = 1`); err != nil {
			return err
		}
		return errBusiness
	})

	require.ErrorIs(t, err, errBusiness)
	assert.Equal(t, 1, calls, "business errors are not retried")
	assert.Equal(t, 0, value(t, db, 1))
}

func TestInTx_SetsLocalTimeouts(t *testing.T) {
	db := openTestDB(t, database.TxSettings{LockTimeout: 1500 * time.Millisecond, StatementTimeout: 4 * time.Second})
	ctx := context.Background()
	var lockTimeout, statementTimeout string

	err := db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SHOW lock_timeout`).Scan(&lockTimeout); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SHOW statement_timeout`).Scan(&statementTimeout)
	})
	require.NoError(t, err)
	assert.Equal(t, "1500ms", lockTimeout)
	assert.Equal(t, "4s", statementTimeout)

	// SET LOCAL ends with the transaction: pooled connections keep the server defaults.
	for range 3 {
		require.NoError(t, db.Pool().QueryRow(ctx, `SHOW lock_timeout`).Scan(&lockTimeout))
		require.NoError(t, db.Pool().QueryRow(ctx, `SHOW statement_timeout`).Scan(&statementTimeout))
		assert.Equal(t, "0", lockTimeout)
		assert.Equal(t, "0", statementTimeout)
	}
}

func TestInTx_DeadlockRetried(t *testing.T) {
	// The default 2s lock timeout is longer than PostgreSQL's 1s deadlock_timeout, so the
	// deadlock detector fires first.
	db := openTestDB(t, database.DefaultTxSettings())
	ctx := context.Background()
	var bothLocked sync.WaitGroup
	bothLocked.Add(2)

	// lockInOrder updates first then second; on the first attempt it waits until the other
	// goroutine holds its first row too, which guarantees the deadlock.
	lockInOrder := func(first, second int) error {
		attempt := 0
		return db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			attempt++
			if _, err := tx.Exec(ctx, `UPDATE t SET v = v + 1 WHERE id = $1`, first); err != nil {
				return err
			}
			if attempt == 1 {
				bothLocked.Done()
				bothLocked.Wait()
			}
			_, err := tx.Exec(ctx, `UPDATE t SET v = v + 1 WHERE id = $1`, second)
			return err
		})
	}

	errs := make(chan error, 2)
	go func() { errs <- lockInOrder(1, 2) }()
	go func() { errs <- lockInOrder(2, 1) }()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	retries, tryAgain := db.observer.counts()
	assert.GreaterOrEqual(t, retries["deadlock"], 1)
	assert.Equal(t, 0, tryAgain)
	assert.Equal(t, 2, value(t, db, 1), "each transaction committed exactly once")
	assert.Equal(t, 2, value(t, db, 2))
}

func TestInTx_LockTimeoutExhausted_ErrTryAgain(t *testing.T) {
	db := openTestDB(t, database.TxSettings{LockTimeout: 200 * time.Millisecond, StatementTimeout: 5 * time.Second, MaxRetries: 2})
	ctx := context.Background()

	holder, err := db.Pool().Begin(ctx)
	require.NoError(t, err)
	defer holder.Rollback(ctx) //nolint:errcheck // test cleanup
	_, err = holder.Exec(ctx, `SELECT * FROM t WHERE id = 1 FOR UPDATE`)
	require.NoError(t, err)

	attempts := 0
	err = db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		attempts++
		_, err := tx.Exec(ctx, `UPDATE t SET v = 1 WHERE id = 1`)
		return err
	})

	require.ErrorIs(t, err, database.ErrTryAgain)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "55P03", pgErr.Code)
	assert.Equal(t, 3, attempts, "MaxRetries+1 attempts")

	retries, tryAgain := db.observer.counts()
	assert.Equal(t, map[string]int{"lock_timeout": 2}, retries)
	assert.Equal(t, 1, tryAgain)
	assert.Equal(t, 1, strings.Count(db.logs.String(), `"msg":"transaction retries exhausted"`))
	assert.Contains(t, db.logs.String(), `"reason":"lock_timeout"`)
}

func TestInTx_SerializationFailureRetried(t *testing.T) {
	db := openTestDB(t, database.DefaultTxSettings())
	calls := 0

	err := db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		calls++
		if _, err := tx.Exec(ctx, `UPDATE t SET v = v + 1 WHERE id = 1`); err != nil {
			return err
		}
		if calls == 1 {
			return &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, 1, value(t, db, 1), "the failed attempt was rolled back")
	retries, _ := db.observer.counts()
	assert.Equal(t, map[string]int{"serialization_failure": 1}, retries)
}

func TestInTx_StatementTimeoutNotRetried(t *testing.T) {
	db := openTestDB(t, database.TxSettings{LockTimeout: time.Second, StatementTimeout: 100 * time.Millisecond, MaxRetries: 3})
	calls := 0

	err := db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		calls++
		_, err := tx.Exec(ctx, `SELECT pg_sleep(1)`)
		return err
	})

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "57014", pgErr.Code)
	assert.NotErrorIs(t, err, database.ErrTryAgain)
	assert.Equal(t, 1, calls)
}

func TestInTx_ContextCanceledDuringBackoff(t *testing.T) {
	db := openTestDB(t, database.DefaultTxSettings())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32

	err := db.InTx(ctx, func(context.Context, pgx.Tx) error {
		calls.Add(1)
		cancel() // canceled before InTx starts waiting for the next attempt
		return &pgconn.PgError{Code: "40P01"}
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load(), "no attempt after cancellation")
}

func TestInTx_PanicRollsBack(t *testing.T) {
	db := openTestDB(t, database.DefaultTxSettings())
	ctx := context.Background()

	assert.PanicsWithValue(t, "boom", func() {
		_ = db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE t SET v = 9 WHERE id = 1`); err != nil {
				return err
			}
			panic("boom")
		})
	})
	assert.Equal(t, 0, value(t, db, 1))

	err := db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE t SET v = 3 WHERE id = 1`)
		return err
	})
	require.NoError(t, err, "the pool still works after a panic")
	assert.Equal(t, 3, value(t, db, 1))
}

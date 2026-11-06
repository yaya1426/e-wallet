// Package database opens the PostgreSQL connection pool, runs units of work (InTx) and
// schema migrations.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TxSettings controls every transaction run by InTx (TDR-010 §5).
type TxSettings struct {
	LockTimeout      time.Duration // SET LOCAL lock_timeout
	StatementTimeout time.Duration // SET LOCAL statement_timeout
	MaxRetries       int           // extra attempts after the first (3 means at most 4 attempts)
}

// DefaultTxSettings returns the TDR-010 defaults. The service takes its values from config;
// this is for tests and tools.
func DefaultTxSettings() TxSettings {
	return TxSettings{LockTimeout: 2 * time.Second, StatementTimeout: 5 * time.Second, MaxRetries: 3}
}

func (s TxSettings) validate() error {
	var errs []error
	if s.LockTimeout <= 0 {
		errs = append(errs, fmt.Errorf("lock timeout must be positive, got %s", s.LockTimeout))
	}
	if s.StatementTimeout <= 0 {
		errs = append(errs, fmt.Errorf("statement timeout must be positive, got %s", s.StatementTimeout))
	}
	if s.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("max retries must not be negative, got %d", s.MaxRetries))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("database: invalid tx settings: %w", err)
	}
	return nil
}

// TxObserver receives InTx retry events, e.g. to count them in metrics.
type TxObserver interface {
	// ObserveTxRetry is called before each retry; reason is "serialization_failure",
	// "deadlock" or "lock_timeout".
	ObserveTxRetry(reason string)
	// ObserveTxTryAgain is called when the retries ran out and InTx returns ErrTryAgain.
	ObserveTxTryAgain()
}

// Options configures Open. Logger and Observer may be nil.
type Options struct {
	Tx       TxSettings
	Logger   *slog.Logger
	Observer TxObserver
}

// DB is the connection pool plus the unit-of-work settings.
type DB struct {
	pool     *pgxpool.Pool
	tx       TxSettings
	log      *slog.Logger
	observer TxObserver
	backoff  func() time.Duration // wait between attempts; replaced in tests
}

// Open creates a connection pool for url and verifies that the database answers.
func Open(ctx context.Context, url string, opts Options) (*DB, error) {
	if err := opts.Tx.validate(); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database: parse url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	observer := opts.Observer
	if observer == nil {
		observer = noopObserver{}
	}
	return &DB{pool: pool, tx: opts.Tx, log: log, observer: observer, backoff: retryBackoff}, nil
}

// Pool returns the underlying pool, for reads outside a unit of work.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Ping checks that the database answers.
func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// Close closes all connections of the pool.
func (db *DB) Close() { db.pool.Close() }

type noopObserver struct{}

func (noopObserver) ObserveTxRetry(string) {}
func (noopObserver) ObserveTxTryAgain()    {}

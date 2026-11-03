package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver used by golang-migrate

	"github.com/yaya1426/e-wallet/migrations"
)

// ErrDirty means a migration failed half-way and the schema must be repaired by hand
// before any further migration runs (TDR-014).
var ErrDirty = errors.New("database: schema version is dirty")

// MigrationStatus describes the schema version of the database against the embedded migrations.
type MigrationStatus struct {
	Version uint // last applied version, 0 when nothing is applied
	Dirty   bool
	Latest  uint // highest embedded version
}

// Pending reports whether embedded migrations exist that are not applied yet.
func (s MigrationStatus) Pending() bool {
	return s.Version < s.Latest
}

// Migrator applies the embedded migrations with golang-migrate (TDR-014).
type Migrator struct {
	pool   *pgxpool.Pool
	url    string
	latest uint
}

// NewMigrator returns a Migrator for the database at url. pool is used for read-only
// status queries; migrations run on their own connection.
func NewMigrator(pool *pgxpool.Pool, url string) (*Migrator, error) {
	latest, err := latestVersion(migrations.FS)
	if err != nil {
		return nil, err
	}
	return &Migrator{pool: pool, url: url, latest: latest}, nil
}

// Up applies all pending migrations. No pending migration is not an error.
func (m *Migrator) Up(ctx context.Context) error {
	runner, err := m.newRunner()
	if err != nil {
		return err
	}
	defer runner.Close() //nolint:errcheck // close errors after a finished run carry no information

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			runner.GracefulStop <- true
		case <-done:
		}
	}()

	err = runner.Up()
	if ctx.Err() != nil {
		// golang-migrate returns nil after a graceful stop, so check the context ourselves.
		return fmt.Errorf("database: migrate up interrupted: %w", ctx.Err())
	}
	var dirty migrate.ErrDirty
	switch {
	case err == nil, errors.Is(err, migrate.ErrNoChange):
		return nil
	case errors.As(err, &dirty):
		return fmt.Errorf("%w: version %d; repair the schema by hand, then run the migrate CLI 'force' command", ErrDirty, dirty.Version)
	default:
		return fmt.Errorf("database: migrate up: %w", err)
	}
}

// Status reads the applied schema version without changing the database.
func (m *Migrator) Status(ctx context.Context) (MigrationStatus, error) {
	status := MigrationStatus{Latest: m.latest}

	var exists bool
	err := m.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, migratepgx.DefaultMigrationsTable).Scan(&exists)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("database: read migration table: %w", err)
	}
	if !exists {
		return status, nil
	}

	var version int64
	err = m.pool.QueryRow(ctx, `SELECT version, dirty FROM `+migratepgx.DefaultMigrationsTable+` LIMIT 1`).Scan(&version, &status.Dirty)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return status, nil
	case err != nil:
		return MigrationStatus{}, fmt.Errorf("database: read migration version: %w", err)
	}
	if version > 0 {
		status.Version = uint(version)
	}
	return status, nil
}

func (m *Migrator) newRunner() (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("database: open migrations: %w", err)
	}
	db, err := sql.Open("pgx", m.url)
	if err != nil {
		return nil, fmt.Errorf("database: open migration connection: %w", err)
	}
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: init migration driver: %w", err)
	}
	runner, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("database: init migrate: %w", err)
	}
	return runner, nil
}

// latestVersion returns the highest migration version in fsys.
func latestVersion(fsys fs.FS) (uint, error) {
	src, err := iofs.New(fsys, ".")
	if err != nil {
		return 0, fmt.Errorf("database: open migrations: %w", err)
	}
	defer src.Close() //nolint:errcheck // read-only embedded source

	version, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("database: read first migration: %w", err)
	}
	for {
		next, err := src.Next(version)
		if errors.Is(err, fs.ErrNotExist) {
			return version, nil
		}
		if err != nil {
			return 0, fmt.Errorf("database: read migration after %d: %w", version, err)
		}
		version = next
	}
}

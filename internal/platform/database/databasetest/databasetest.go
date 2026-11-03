//go:build integration

// Package databasetest starts PostgreSQL in Docker (testcontainers) for integration tests.
package databasetest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image is the PostgreSQL image used in tests; it matches docker compose and CI (TDR-002).
const Image = "postgres:18-alpine"

// Postgres is a running PostgreSQL container.
type Postgres struct {
	container *tcpostgres.PostgresContainer
	// URL connects to the container's default database.
	URL string
}

var (
	shared     *Postgres
	sharedErr  error
	sharedOnce sync.Once
	databaseID atomic.Int64
)

// Shared returns one container per test binary, started on first use. Tests isolate
// themselves with NewDatabase. The container is removed by the testcontainers reaper when
// the test binary exits.
func Shared(t testing.TB) *Postgres {
	t.Helper()
	sharedOnce.Do(func() {
		shared, sharedErr = start(context.Background())
	})
	if sharedErr != nil {
		t.Fatalf("databasetest: start shared postgres: %v", sharedErr)
	}
	return shared
}

// Start returns a dedicated container that is terminated when the test ends. Use it for
// tests that stop or break the server.
func Start(t testing.TB) *Postgres {
	t.Helper()
	pg, err := start(context.Background())
	if err != nil {
		t.Fatalf("databasetest: start postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg.container); err != nil {
			t.Logf("databasetest: terminate container: %v", err)
		}
	})
	return pg
}

// Stop stops the container, making the database unreachable.
func (p *Postgres) Stop(t testing.TB) {
	t.Helper()
	timeout := 10 * time.Second
	if err := p.container.Stop(context.Background(), &timeout); err != nil {
		t.Fatalf("databasetest: stop container: %v", err)
	}
}

// NewDatabase creates an empty database in the container, drops it when the test ends,
// and returns its URL.
func (p *Postgres) NewDatabase(t testing.TB) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), databaseID.Add(1))

	admin, err := pgx.Connect(ctx, p.URL)
	if err != nil {
		t.Fatalf("databasetest: connect: %v", err)
	}
	defer admin.Close(ctx) //nolint:errcheck // test helper connection
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("databasetest: create database: %v", err)
	}

	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, p.URL)
		if err != nil {
			return // container already gone
		}
		defer conn.Close(ctx) //nolint:errcheck // test helper connection
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	u, err := url.Parse(p.URL)
	if err != nil {
		t.Fatalf("databasetest: parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func start(ctx context.Context) (*Postgres, error) {
	container, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("wallet"),
		tcpostgres.WithUsername("wallet"),
		tcpostgres.WithPassword("wallet"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, err
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		return nil, err
	}
	return &Postgres{container: container, URL: dsn}, nil
}

//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
)

// newTestApp wires the wallet handler exactly like serve does, against the database at url.
func newTestApp(t *testing.T, url string) http.Handler {
	t.Helper()
	pool, migrator, err := connect(context.Background(), config.Wallet{DatabaseURL: url})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	log := logger.New(io.Discard, slog.LevelInfo)
	return newHandler(log, metrics.New(), readinessChecks(log, pool, migrator))
}

func getReadyz(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec
}

func TestReadyz_MigrationsApplied_OK(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, stderr := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)

	rec := getReadyz(t, newTestApp(t, url))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}`, rec.Body.String())
}

func TestReadyz_MigrationsPending_Unavailable(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)

	rec := getReadyz(t, newTestApp(t, url))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","checks":{"database":"ok","migrations":"pending"}}`, rec.Body.String())
}

func TestReadyz_DirtyMigration_Fail(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, _ := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code)
	pool, err := database.Open(context.Background(), url)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`)
	require.NoError(t, err)

	rec := getReadyz(t, newTestApp(t, url))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","checks":{"database":"ok","migrations":"fail"}}`, rec.Body.String())
}

func TestReadyz_ContainerStopped_Unavailable(t *testing.T) {
	pg := databasetest.Start(t)
	code, _, _ := runCLI(t, map[string]string{"DATABASE_URL": pg.URL}, "migrate", "up")
	require.Equal(t, exitOK, code)
	h := newTestApp(t, pg.URL)
	require.Equal(t, http.StatusOK, getReadyz(t, h).Code)

	pg.Stop(t)

	rec := getReadyz(t, h)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","checks":{"database":"fail","migrations":"fail"}}`, rec.Body.String())

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, health.Code, "the process keeps serving")
}

func TestMigrateCommands_UpIdempotentAndStatus(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	environ := map[string]string{"DATABASE_URL": url}

	code, stdout, _ := runCLI(t, environ, "migrate", "status")
	require.Equal(t, exitOK, code)
	assert.Equal(t, "version=0 dirty=false latest=1\n", stdout)

	code, _, stderr := runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	code, _, stderr = runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code, "second run is a no-op: %s", stderr)

	code, stdout, _ = runCLI(t, environ, "migrate", "status")
	require.Equal(t, exitOK, code)
	assert.Equal(t, "version=1 dirty=false latest=1\n", stdout)
}

func TestMigrateUp_DirtyVersion_Exit1(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	environ := map[string]string{"DATABASE_URL": url}
	code, _, _ := runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code)
	pool, err := database.Open(context.Background(), url)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`)
	require.NoError(t, err)

	code, stdout, _ := runCLI(t, environ, "migrate", "up")

	assert.Equal(t, exitError, code)
	assert.Contains(t, stdout, "dirty")
	assert.Contains(t, stdout, "force")
}

func TestServe_CancelledContext_ExitsZero(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve"}, map[string]string{
			"DATABASE_URL": url,
			"HTTP_ADDR":    "127.0.0.1:0",
		}, io.Discard, io.Discard)
	}()

	time.Sleep(500 * time.Millisecond) // let serve connect and start listening
	cancel()                           // what SIGTERM does via signal.NotifyContext

	select {
	case code := <-done:
		assert.Equal(t, exitOK, code)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}

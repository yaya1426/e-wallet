//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/banksim"
	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
	"github.com/yaya1426/e-wallet/internal/webhook"
	"github.com/yaya1426/e-wallet/migrations"
)

// newTestApp wires the wallet handler exactly like serve does, against the database at url.
func newTestApp(t *testing.T, url string) http.Handler {
	t.Helper()
	cfg, err := config.LoadWallet(map[string]string{"DATABASE_URL": url})
	require.NoError(t, err)
	log := logger.New(io.Discard, slog.LevelInfo)
	m := metrics.New()
	db, migrator, err := connect(context.Background(), cfg, log, m)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	return newHandler(log, m, readinessChecks(log, db, migrator), nil)
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
	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Pool().Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`)
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
	assert.Equal(t, fmt.Sprintf("version=0 dirty=false latest=%d\n", latestMigration(t)), stdout)

	code, _, stderr := runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	code, _, stderr = runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code, "second run is a no-op: %s", stderr)

	code, stdout, _ = runCLI(t, environ, "migrate", "status")
	require.Equal(t, exitOK, code)
	assert.Equal(t, fmt.Sprintf("version=%[1]d dirty=false latest=%[1]d\n", latestMigration(t)), stdout)
}

func TestMigrateUp_DirtyVersion_Exit1(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	environ := map[string]string{"DATABASE_URL": url}
	code, _, _ := runCLI(t, environ, "migrate", "up")
	require.Equal(t, exitOK, code)
	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Pool().Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`)
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
			"DATABASE_URL":        url,
			"HTTP_ADDR":           "127.0.0.1:0",
			"JWT_SECRET":          testJWTSecret,
			"BANK_BASE_URL":       testBankURL,
			"BANK_WEBHOOK_SECRET": testWebhookSecret,
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

// TestServe_OutboxDisabled: with OUTBOX_ENABLED=false serve does not deliver outbox events;
// with the default it does (AC-8).
func TestServe_OutboxDisabled(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, stderr := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()
	// A due event of a type no handler knows yet: delivering it means claiming it (attempts + 1).
	_, err = db.Pool().Exec(context.Background(), `INSERT INTO outbox_events
		(id, aggregate_type, aggregate_id, event_type, payload, status, next_attempt_at, created_at)
		VALUES (gen_random_uuid(), 'transaction', gen_random_uuid(), 'test.event', '{}', 'PENDING', now() - interval '1 second', now())`)
	require.NoError(t, err)
	attempts := func() int {
		var n int
		require.NoError(t, db.Pool().QueryRow(context.Background(), `SELECT attempts FROM outbox_events`).Scan(&n))
		return n
	}
	serveFor := func(enabled string, until func() bool) string {
		ctx, cancel := context.WithCancel(context.Background())
		var out bytes.Buffer
		done := make(chan int, 1)
		go func() {
			done <- run(ctx, []string{"serve"}, map[string]string{
				"DATABASE_URL": url, "HTTP_ADDR": "127.0.0.1:0", "JWT_SECRET": testJWTSecret, "BANK_BASE_URL": testBankURL,
				"BANK_WEBHOOK_SECRET": testWebhookSecret, "OUTBOX_ENABLED": enabled, "QUEUE_POLL_INTERVAL": "50ms",
			}, &out, io.Discard)
		}()
		until()
		cancel()
		select {
		case code := <-done:
			require.Equal(t, exitOK, code)
		case <-time.After(15 * time.Second):
			t.Fatal("serve did not stop after cancel")
		}
		return out.String()
	}

	logs := serveFor("false", func() bool { time.Sleep(time.Second); return true })
	assert.Equal(t, 0, attempts(), "a disabled instance does not claim events")
	assert.Contains(t, logs, "outbox delivery disabled")

	logs = serveFor("true", func() bool {
		return assert.Eventually(t, func() bool { return attempts() >= 1 }, 10*time.Second, 20*time.Millisecond)
	})
	assert.Contains(t, logs, "outbox delivery started")
}

// latestMigration counts the embedded up migrations; versions are sequential from 1 (TDR-014).
func latestMigration(t *testing.T) int {
	t.Helper()
	ups, err := fs.Glob(migrations.FS, "*.up.sql")
	require.NoError(t, err)
	return len(ups)
}

// TestServe_Inbox: serve mounts POST /webhooks/bank and stores signed events. With
// INBOX_ENABLED=false they wait as PENDING (T015 AC-8); by default the inbox runs with the
// payment handler (T016), so an event that can never be applied ends FAILED.
func TestServe_Inbox(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, stderr := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()

	serveWith := func(enabled string, use func(base string)) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		ctx, cancel := context.WithCancel(context.Background())
		var out syncBuffer
		done := make(chan int, 1)
		go func() {
			done <- run(ctx, []string{"serve"}, map[string]string{
				"DATABASE_URL": url, "HTTP_ADDR": addr, "JWT_SECRET": testJWTSecret, "BANK_BASE_URL": testBankURL,
				"BANK_WEBHOOK_SECRET": testWebhookSecret, "INBOX_ENABLED": enabled, "QUEUE_POLL_INTERVAL": "50ms",
			}, &out, io.Discard)
		}()
		base := "http://" + addr
		require.Eventually(t, func() bool {
			resp, err := http.Get(base + "/healthz")
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 10*time.Second, 20*time.Millisecond)
		use(base)
		cancel()
		select {
		case code := <-done:
			require.Equal(t, exitOK, code)
		case <-time.After(15 * time.Second):
			t.Fatal("serve did not stop after cancel")
		}
		return out.String()
	}
	postEvent := func(base, eventID string) int {
		body := []byte(`{"event_id":"` + eventID + `","event_type":"debit.succeeded"}`)
		req, err := http.NewRequest(http.MethodPost, base+"/webhooks/bank", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set(webhook.SignatureHeader, banksim.Sign([]byte(testWebhookSecret), time.Now(), body))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	statusOf := func(eventID string) string {
		var s string
		require.NoError(t, db.Pool().QueryRow(context.Background(),
			`SELECT status FROM inbox_events WHERE provider_event_id = $1`, eventID).Scan(&s))
		return s
	}

	logs := serveWith("false", func(base string) {
		assert.Equal(t, http.StatusOK, postEvent(base, "evt-disabled"))
		time.Sleep(300 * time.Millisecond)
	})
	assert.Equal(t, "PENDING", statusOf("evt-disabled"), "stored, not processed")
	assert.Contains(t, logs, `"msg":"inbox processing disabled"`)
	assert.Contains(t, logs, "INBOX_ENABLED=false")

	logs = serveWith("true", func(base string) {
		assert.Equal(t, http.StatusOK, postEvent(base, "evt-incomplete"))
		assert.Eventually(t, func() bool { return statusOf("evt-incomplete") == "FAILED" }, 10*time.Second, 20*time.Millisecond,
			"processed: the event lacks the fields needed to apply it")
	})
	assert.Contains(t, logs, `"msg":"inbox processing started"`)
}

// syncBuffer is a bytes.Buffer safe for the serve goroutine and the test.
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

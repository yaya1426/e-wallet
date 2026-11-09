package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
)

// testJWTSecret is a 32-byte secret for tests that start serve.
const testJWTSecret = "test-jwt-secret-of-32-bytes-long"

// testBankURL is a BANK_BASE_URL for tests that start serve; nothing calls it yet.
const testBankURL = "http://127.0.0.1:9"

// testWebhookSecret is a 32-byte BANK_WEBHOOK_SECRET for tests that start serve.
const testWebhookSecret = "test-webhook-secret-of-32-bytes!"

func runCLI(t *testing.T, environ map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, environ, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCLI_UnknownCommand_Exit2(t *testing.T) {
	code, _, stderr := runCLI(t, map[string]string{}, "foo")

	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr, `unknown command "foo"`)
	assert.Contains(t, stderr, "usage:")
}

func TestCLI_UsageErrors_Exit2(t *testing.T) {
	tests := [][]string{
		{"migrate"},
		{"migrate", "sideways"},
		{"migrate", "up", "extra"},
		{"serve", "extra"},
		{"--unknown-flag"},
		{"serve", "--unknown-flag"},
	}
	for _, args := range tests {
		code, _, stderr := runCLI(t, map[string]string{}, args...)
		assert.Equal(t, exitUsage, code, "args %v", args)
		assert.Contains(t, stderr, "usage:", "args %v", args)
	}
}

func TestCLI_Help_Exit0(t *testing.T) {
	code, stdout, stderr := runCLI(t, map[string]string{}, "-h")

	assert.Equal(t, exitOK, code)
	assert.Contains(t, stdout, "usage:")
	assert.Empty(t, stderr)
}

func TestServe_InvalidConfig_Exit1NamesVariable(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		environ map[string]string
		wantVar string
	}{
		{"serve default, no DATABASE_URL", nil, map[string]string{}, "DATABASE_URL"},
		{"serve, bad LOG_LEVEL", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{"serve, bad SHUTDOWN_TIMEOUT", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "SHUTDOWN_TIMEOUT": "soon"}, "SHUTDOWN_TIMEOUT"},
		{"serve, zero DB_LOCK_TIMEOUT", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "DB_LOCK_TIMEOUT": "0s"}, "DB_LOCK_TIMEOUT"},
		{"serve, bad DB_STATEMENT_TIMEOUT", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "DB_STATEMENT_TIMEOUT": "later"}, "DB_STATEMENT_TIMEOUT"},
		{"serve, negative DB_TX_MAX_RETRIES", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "DB_TX_MAX_RETRIES": "-1"}, "DB_TX_MAX_RETRIES"},
		{"serve, no JWT_SECRET", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x"}, "JWT_SECRET"},
		{"serve, 31-byte JWT_SECRET", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": testJWTSecret[:31]}, "JWT_SECRET"},
		{"serve, zero JWT_TTL", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": testJWTSecret, "JWT_TTL": "0s"}, "JWT_TTL"},
		{"serve, no BANK_BASE_URL", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": testJWTSecret}, "BANK_BASE_URL"},
		{"serve, 31-byte BANK_WEBHOOK_SECRET", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": testJWTSecret, "BANK_BASE_URL": testBankURL, "BANK_WEBHOOK_SECRET": testWebhookSecret[:31]}, "BANK_WEBHOOK_SECRET"},
		{"serve, bad INBOX_WORKERS", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "INBOX_WORKERS": "0"}, "INBOX_WORKERS"},
		{"serve, P2P min above max", []string{"serve"}, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": testJWTSecret, "LIMIT_P2P_MIN": "500", "LIMIT_P2P_MAX": "400"}, "LIMIT_P2P_MIN"},
		{"migrate up, no DATABASE_URL", []string{"migrate", "up"}, map[string]string{}, "DATABASE_URL"},
		{"migrate status, no DATABASE_URL", []string{"migrate", "status"}, map[string]string{}, "DATABASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, tt.environ, tt.args...)

			assert.Equal(t, exitError, code)
			assert.Contains(t, stderr, tt.wantVar)
		})
	}
}

func TestServe_DatabaseUnreachable_Exit1(t *testing.T) {
	code, stdout, _ := runCLI(t, map[string]string{
		"DATABASE_URL":        "postgres://wallet:wallet@127.0.0.1:1/wallet?sslmode=disable&connect_timeout=1",
		"JWT_SECRET":          testJWTSecret,
		"BANK_BASE_URL":       testBankURL,
		"BANK_WEBHOOK_SECRET": testWebhookSecret,
	}, "serve")

	assert.Equal(t, exitError, code)
	assert.Contains(t, stdout, "startup failed")
}

func TestMetrics_Exposed(t *testing.T) {
	h := newHandler(logger.New(io.Discard, slog.LevelInfo), metrics.New(), nil, nil)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "go_goroutines")
	assert.Contains(t, rec.Body.String(), `http_requests_total{method="GET",route="GET /healthz",status="200"} 1`)
	assert.Contains(t, rec.Body.String(), "db_tx_try_again_total 0")
	assert.Contains(t, rec.Body.String(), `db_tx_retries_total{reason="deadlock"} 0`)
}

func TestHealthz_RouteServed(t *testing.T) {
	h := newHandler(logger.New(io.Discard, slog.LevelInfo), metrics.New(), nil, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

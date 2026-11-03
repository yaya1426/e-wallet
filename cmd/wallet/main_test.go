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
		"DATABASE_URL": "postgres://wallet:wallet@127.0.0.1:1/wallet?sslmode=disable&connect_timeout=1",
	}, "serve")

	assert.Equal(t, exitError, code)
	assert.Contains(t, stdout, "startup failed")
}

func TestMetrics_Exposed(t *testing.T) {
	h := newHandler(logger.New(io.Discard, slog.LevelInfo), metrics.New(), nil)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "go_goroutines")
	assert.Contains(t, rec.Body.String(), `http_requests_total{method="GET",route="GET /healthz",status="200"} 1`)
}

func TestHealthz_RouteServed(t *testing.T) {
	h := newHandler(logger.New(io.Discard, slog.LevelInfo), metrics.New(), nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

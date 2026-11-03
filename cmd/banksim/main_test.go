package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yaya1426/e-wallet/internal/platform/logger"
)

func TestHealthz_OK(t *testing.T) {
	rec := httptest.NewRecorder()

	newHandler(logger.New(io.Discard, slog.LevelInfo)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestRun_InvalidConfig_Exit1(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), map[string]string{"LOG_LEVEL": "loud"}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "LOG_LEVEL")
}

func TestRun_CancelledContext_ExitsZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, map[string]string{"BANKSIM_HTTP_ADDR": "127.0.0.1:0"}, io.Discard, io.Discard)
	}()

	time.Sleep(200 * time.Millisecond) // let the server start listening
	cancel()

	select {
	case code := <-done:
		assert.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("banksim did not stop after cancel")
	}
}

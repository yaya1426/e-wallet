package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/logger"
)

func TestServer_GracefulShutdown_FinishesInFlight(t *testing.T) {
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		WriteJSON(w, http.StatusOK, map[string]string{"done": "yes"})
	})
	log := logger.New(io.Discard, slog.LevelInfo)
	srv := New("127.0.0.1:0", NewHandler(mux, log, nil), 5*time.Second, log)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, ln) }()

	type result struct {
		status int
		err    error
	}
	inFlight := make(chan result, 1)
	go func() {
		resp, err := get(addr, "/slow")
		if err != nil {
			inFlight <- result{err: err}
			return
		}
		_ = resp.Body.Close()
		inFlight <- result{status: resp.StatusCode}
	}()

	<-started
	cancel() // SIGTERM equivalent: the root context ends

	select {
	case err := <-serveDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop within the shutdown timeout")
	}

	res := <-inFlight
	require.NoError(t, res.err)
	assert.Equal(t, http.StatusOK, res.status, "in-flight request completes")

	_, err = net.DialTimeout("tcp", addr, time.Second)
	assert.Error(t, err, "new connections are refused after shutdown")
}

func TestServer_ShutdownTimeoutExceeded(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stuck", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() { close(release) })
	log := logger.New(io.Discard, slog.LevelInfo)
	srv := New("127.0.0.1:0", NewHandler(mux, log, nil), 100*time.Millisecond, log)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, ln) }()

	clientErr := make(chan error, 1)
	go func() {
		resp, err := get(ln.Addr().String(), "/stuck")
		if err == nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()
	<-started
	cancel()

	select {
	case err := <-serveDone:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the shutdown timeout")
	}
	select {
	case err := <-clientErr:
		assert.Error(t, err, "the stuck request's connection is closed")
	case <-time.After(5 * time.Second):
		t.Fatal("stuck connection was not closed after the shutdown timeout")
	}
}

func TestServer_RunFailsOnBusyAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	log := logger.New(io.Discard, slog.LevelInfo)
	srv := New(ln.Addr().String(), http.NewServeMux(), time.Second, log)

	err = srv.Run(context.Background())
	assert.ErrorContains(t, err, "listen")
}

func get(addr, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

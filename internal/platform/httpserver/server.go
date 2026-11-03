// Package httpserver provides the HTTP server, the standard middleware chain, the JSON
// error body (TDR-013) and the health endpoints shared by every process.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server timeouts protect against slow or idle clients.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

// Server is an http.Server that shuts down gracefully when its context ends.
type Server struct {
	srv             *http.Server
	shutdownTimeout time.Duration
	log             *slog.Logger
}

// New returns a Server for addr. On shutdown, in-flight requests get up to
// shutdownTimeout to finish.
func New(addr string, handler http.Handler, shutdownTimeout time.Duration, log *slog.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		shutdownTimeout: shutdownTimeout,
		log:             log,
	}
}

// Run listens on the server address and serves until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen on %s: %w", s.srv.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx ends, then stops accepting connections and waits for
// in-flight requests up to the shutdown timeout.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.srv.Serve(ln)
	}()
	s.log.InfoContext(ctx, "http server started", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return fmt.Errorf("httpserver: serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("http server shutting down", slog.Duration("timeout", s.shutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		// Requests still running after the timeout are cut off so the process can exit.
		_ = s.srv.Close()
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("httpserver: serve: %w", err)
	}
	s.log.Info("http server stopped")
	return nil
}

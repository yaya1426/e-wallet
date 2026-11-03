// Command banksim simulates the external bank for development and tests (TDR-008).
// It is test tooling and must never be deployed to production. For now it only
// serves /healthz; the bank behaviour comes in a later task.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // Africa/Cairo must resolve in distroless images (TDR-012)

	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
)

// shutdownTimeout is how long in-flight requests get to finish on SIGTERM.
const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, nil, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run serves until ctx ends and returns the process exit code. A nil environ reads the
// process environment.
func run(ctx context.Context, environ map[string]string, stdout, stderr io.Writer) int {
	cfg, err := config.LoadBanksim(environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "banksim: %v\n", err)
		return 1
	}
	log := logger.New(stdout, cfg.LogLevel)

	srv := httpserver.New(cfg.HTTPAddr, newHandler(log), shutdownTimeout, log)
	if err := srv.Run(ctx); err != nil {
		log.Error("banksim stopped with error", slog.Any("error", err))
		return 1
	}
	return 0
}

func newHandler(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpserver.Healthz())
	return httpserver.NewHandler(mux, log, nil)
}

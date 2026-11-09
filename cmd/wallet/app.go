package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

const (
	// startupTimeout bounds connecting to the database when a command starts.
	startupTimeout = 10 * time.Second
	// readinessCheckTimeout bounds each /readyz check.
	readinessCheckTimeout = 2 * time.Second
)

// serve runs the HTTP API until ctx ends.
func serve(ctx context.Context, environ map[string]string, stdout, stderr io.Writer) int {
	cfg, log, ok := setup(environ, stdout, stderr)
	if !ok {
		return exitError
	}
	if err := cfg.ValidateServe(); err != nil {
		_, _ = fmt.Fprintf(stderr, "wallet: %v\n", err)
		return exitError
	}
	m := metrics.New()
	db, migrator, err := connect(ctx, cfg, log, m)
	if err != nil {
		log.Error("startup failed", slog.Any("error", err))
		return exitError
	}
	defer db.Close()

	api, err := newAPI(db, cfg, clock.System{}, log, 0)
	if err != nil {
		log.Error("startup failed", slog.Any("error", err))
		return exitError
	}
	srv := httpserver.New(cfg.HTTPAddr, newHandler(log, m, readinessChecks(log, db, migrator), api.routes), cfg.ShutdownTimeout, log)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return srv.Run(gctx) })
	if err := g.Wait(); err != nil {
		log.Error("wallet stopped with error", slog.Any("error", err))
		return exitError
	}
	return exitOK
}

// migrateUp applies all pending migrations.
func migrateUp(ctx context.Context, environ map[string]string, stdout, stderr io.Writer) int {
	cfg, log, ok := setup(environ, stdout, stderr)
	if !ok {
		return exitError
	}
	db, migrator, err := connect(ctx, cfg, log, nil)
	if err != nil {
		log.Error("migrate up failed", slog.Any("error", err))
		return exitError
	}
	defer db.Close()

	if err := migrator.Up(ctx); err != nil {
		log.Error("migrate up failed", slog.Any("error", err))
		return exitError
	}
	status, err := migrator.Status(ctx)
	if err != nil {
		log.Error("migrate up: read status", slog.Any("error", err))
		return exitError
	}
	log.Info("migrations applied", slog.Uint64("version", uint64(status.Version)))
	return exitOK
}

// migrateStatus prints the schema version of the database.
func migrateStatus(ctx context.Context, environ map[string]string, stdout, stderr io.Writer) int {
	cfg, log, ok := setup(environ, stdout, stderr)
	if !ok {
		return exitError
	}
	db, migrator, err := connect(ctx, cfg, log, nil)
	if err != nil {
		log.Error("migrate status failed", slog.Any("error", err))
		return exitError
	}
	defer db.Close()

	status, err := migrator.Status(ctx)
	if err != nil {
		log.Error("migrate status failed", slog.Any("error", err))
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "version=%d dirty=%t latest=%d\n", status.Version, status.Dirty, status.Latest)
	return exitOK
}

// setup loads the configuration and builds the logger. Configuration errors go to stderr
// because the log level is not known yet.
func setup(environ map[string]string, stdout, stderr io.Writer) (config.Wallet, *slog.Logger, bool) {
	cfg, err := config.LoadWallet(environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "wallet: %v\n", err)
		return config.Wallet{}, nil, false
	}
	return cfg, logger.New(stdout, cfg.LogLevel), true
}

// connect opens the database and the migrator. observer may be nil.
func connect(ctx context.Context, cfg config.Wallet, log *slog.Logger, observer database.TxObserver) (*database.DB, *database.Migrator, error) {
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	db, err := database.Open(startCtx, cfg.DatabaseURL, database.Options{
		Tx: database.TxSettings{
			LockTimeout:      cfg.DBLockTimeout,
			StatementTimeout: cfg.DBStatementTimeout,
			MaxRetries:       cfg.DBTxMaxRetries,
		},
		Logger:   log,
		Observer: observer,
	})
	if err != nil {
		return nil, nil, err
	}
	migrator, err := database.NewMigrator(db.Pool(), cfg.DatabaseURL)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, migrator, nil
}

// newHandler builds the routes of the wallet HTTP API: the ops endpoints plus whatever
// mount adds (nil for none).
func newHandler(log *slog.Logger, m *metrics.Metrics, checks []httpserver.ReadinessCheck, mount func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpserver.Healthz())
	mux.Handle("GET /readyz", httpserver.Readyz(readinessCheckTimeout, checks...))
	mux.Handle("GET /metrics", m.Handler())
	if mount != nil {
		mount(mux)
	}
	return httpserver.NewHandler(mux, log, m)
}

// api is the composition root of the business modules (architecture §3): it builds the
// services in dependency order (ledger → wallet → identity) and their HTTP handlers.
type api struct {
	identity *identity.Handler
	wallet   *wallet.Handler
}

// newAPI wires the modules. bcryptCost 0 means identity.DefaultBcryptCost (tests pass a
// lower cost to stay fast).
func newAPI(db *database.DB, cfg config.Wallet, clk clock.Clock, log *slog.Logger, bcryptCost int) (*api, error) {
	ledgerSvc := ledger.NewService(db, clk)
	walletSvc := wallet.NewService(db, ledgerSvc, clk)
	identitySvc, err := identity.NewService(db, walletSvc, clk, identity.Config{
		JWTSecret: cfg.JWTSecret, JWTTTL: cfg.JWTTTL, BcryptCost: bcryptCost,
	})
	if err != nil {
		return nil, err
	}
	return &api{
		identity: identity.NewHandler(identitySvc, identity.NewLoginLimiter(clk), log),
		wallet:   wallet.NewHandler(walletSvc, log),
	}, nil
}

// routes mounts every business endpoint.
func (a *api) routes(mux *http.ServeMux) {
	a.identity.Routes(mux)
	a.wallet.Routes(mux, a.identity.RequireAuth)
}

// readinessChecks reports the database connection and whether migrations are applied
// (TDR-013). Failures are logged so a "fail" in /readyz can be diagnosed.
func readinessChecks(log *slog.Logger, db *database.DB, migrator *database.Migrator) []httpserver.ReadinessCheck {
	return []httpserver.ReadinessCheck{
		{Name: "database", Check: func(ctx context.Context) httpserver.CheckStatus {
			if err := db.Ping(ctx); err != nil {
				log.WarnContext(ctx, "readiness check failed", slog.String("check", "database"), slog.Any("error", err))
				return httpserver.CheckFail
			}
			return httpserver.CheckOK
		}},
		{Name: "migrations", Check: func(ctx context.Context) httpserver.CheckStatus {
			status, err := migrator.Status(ctx)
			switch {
			case err != nil:
				log.WarnContext(ctx, "readiness check failed", slog.String("check", "migrations"), slog.Any("error", err))
				return httpserver.CheckFail
			case status.Dirty:
				log.WarnContext(ctx, "readiness check failed", slog.String("check", "migrations"),
					slog.String("error", "schema version is dirty"), slog.Uint64("version", uint64(status.Version)))
				return httpserver.CheckFail
			case status.Pending():
				return httpserver.CheckPending
			default:
				return httpserver.CheckOK
			}
		}},
	}
}

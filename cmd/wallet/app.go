package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banksim"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
	"github.com/yaya1426/e-wallet/internal/reconciliation"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/transfer"
	"github.com/yaya1426/e-wallet/internal/wallet"
	"github.com/yaya1426/e-wallet/internal/webhook"
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

	gateway, err := banksim.New(banksim.Config{BaseURL: cfg.BankBaseURL, Timeout: cfg.BankHTTPTimeout}, log, m)
	if err != nil {
		log.Error("startup failed", slog.Any("error", err))
		return exitError
	}
	api, err := newAPI(db, cfg, clock.System{}, log, m, 0, gateway)
	if err != nil {
		log.Error("startup failed", slog.Any("error", err))
		return exitError
	}
	srv := httpserver.New(cfg.HTTPAddr, newHandler(log, m, readinessChecks(log, db, migrator), api.routes,
		httpserver.WithClientIPHeader(cfg.ClientIPHeader)), cfg.ShutdownTimeout, log)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return srv.Run(gctx) })
	g.Go(func() error {
		return idempotency.NewCleanup(api.idempotency, db.Pool(), cfg.IdempotencyCleanupInterval, log).Run(gctx)
	})
	if cfg.OutboxEnabled {
		g.Go(func() error { return api.outbox.Run(gctx) })
		log.Info("outbox delivery started", slog.Int("workers", cfg.OutboxWorkers))
	} else {
		// An API-only instance: it still enqueues events, other instances deliver them.
		log.Info("outbox delivery disabled", slog.String("reason", "OUTBOX_ENABLED=false"))
	}
	switch {
	case !cfg.InboxEnabled:
		// An API-only instance: it still stores webhook events, other instances process them.
		log.Info("inbox processing disabled", slog.String("reason", "INBOX_ENABLED=false"))
	case !api.inbox.HasHandler():
		// Events are stored and wait until a handler is wired (payment, T016).
		log.Info("inbox processing disabled", slog.String("reason", "no bank event handler"))
	default:
		g.Go(func() error { return api.inbox.Run(gctx) })
		log.Info("inbox processing started", slog.Int("workers", cfg.InboxWorkers))
	}
	if cfg.InternalAPIKey != "" {
		log.Info("internal API enabled")
	} else {
		log.Info("internal API disabled", slog.String("reason", "INTERNAL_API_KEY not set"))
	}
	if cfg.ReconEnabled {
		g.Go(func() error { return api.recon.Run(gctx) })
		log.Info("reconciliation started", slog.Any("jobs", api.recon.Jobs()))
	} else {
		// Another instance runs the jobs (the advisory locks make that safe either way).
		log.Info("reconciliation disabled", slog.String("reason", "RECON_ENABLED=false"))
	}
	if err := g.Wait(); err != nil {
		log.Error("wallet stopped with error", slog.Any("error", err))
		return exitError
	}
	return exitOK
}

// reconcile runs one reconciliation job once (TDR-009 §1: the same function the scheduler
// runs). It exits 0 when the run succeeded or another instance is running the job, 1 when
// the run failed or the job is unknown.
func reconcile(ctx context.Context, job string, environ map[string]string, stdout, stderr io.Writer) int {
	cfg, log, ok := setup(environ, stdout, stderr)
	if !ok {
		return exitError
	}
	if err := cfg.ValidateBank(); err != nil { // the jobs ask the bank
		_, _ = fmt.Fprintf(stderr, "wallet: %v\n", err)
		return exitError
	}
	db, _, err := connect(ctx, cfg, log, nil)
	if err != nil {
		log.Error("reconcile failed", slog.Any("error", err))
		return exitError
	}
	defer db.Close()
	gateway, err := banksim.New(banksim.Config{BaseURL: cfg.BankBaseURL, Timeout: cfg.BankHTTPTimeout}, log, nil)
	if err != nil {
		log.Error("reconcile failed", slog.Any("error", err))
		return exitError
	}
	a, err := newAPI(db, cfg, clock.System{}, log, nil, 0, gateway)
	if err != nil {
		log.Error("reconcile failed", slog.Any("error", err))
		return exitError
	}
	ran, res, err := a.recon.RunOnce(ctx, job)
	switch {
	case errors.Is(err, reconciliation.ErrUnknownJob):
		_, _ = fmt.Fprintf(stderr, "wallet: %v\n", err)
		return exitError
	case err != nil:
		log.Error("reconcile failed", slog.Any("error", err))
		return exitError
	case !ran:
		_, _ = fmt.Fprintf(stdout, "job %s is running elsewhere; skipped\n", job)
		return exitOK
	case res.Err != nil:
		_, _ = fmt.Fprintf(stdout, "job %s FAILED: checked %d, fixed %d: %v\n", job, res.Checked, res.Fixed, res.Err)
		return exitError
	default:
		_, _ = fmt.Fprintf(stdout, "job %s SUCCESS: checked %d, fixed %d\n", job, res.Checked, res.Fixed)
		return exitOK
	}
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
func newHandler(log *slog.Logger, m *metrics.Metrics, checks []httpserver.ReadinessCheck, mount func(*http.ServeMux),
	opts ...httpserver.Option,
) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpserver.Healthz())
	mux.Handle("GET /readyz", httpserver.Readyz(readinessCheckTimeout, checks...))
	mux.Handle("GET /metrics", m.Handler())
	if mount != nil {
		mount(mux)
	}
	return httpserver.NewHandler(mux, log, m, opts...)
}

// api is the composition root of the business modules (architecture §3): it builds the
// services in dependency order (ledger → wallet → identity) and their HTTP handlers.
type api struct {
	identity    *identity.Handler
	wallet      *wallet.Handler
	transfer    *transfer.Handler
	payment     *payment.Handler
	history     *transaction.Handler
	idempotency *idempotency.Service // used by the money endpoints and the cleanup job
	outbox      *outbox.Service      // delivered by serve when OUTBOX_ENABLED
	inbox       *webhook.Service     // processed by serve when INBOX_ENABLED and a handler is set
	webhook     *webhook.Handler
	recon       *reconciliation.Scheduler // run by serve when RECON_ENABLED, or once by `reconcile`
	operator    operatorRoutes            // /internal/v1/*, mounted when internalKey is set
	internalKey string
}

// operatorRoutes are the modules' /internal/v1 endpoints (TDR-018 §5).
type operatorRoutes struct {
	discrepancies *discrepancy.Handler
	wallets       *wallet.Handler
}

// observer is what the modules report to: the metrics.
type observer interface {
	outbox.Observer
	webhook.Observer
	reconciliation.Observer
	reconciliation.StuckObserver
}

// reconJobs returns the reconciliation jobs to register (TDR-009 §2). A variable so tests can
// register test jobs instead.
var reconJobs = func(d reconciliation.Deps, cfg config.Wallet) []reconciliation.Job {
	return []reconciliation.Job{
		reconciliation.PendingBankOps(d, reconciliation.PendingBankOpsConfig{
			Interval: cfg.ReconPendingInterval, MinAge: cfg.ReconPendingMinAge,
			Recheck: cfg.ReconPendingRecheck, Batch: cfg.ReconPendingBatch,
		}),
		reconciliation.DeadOutbox(d, reconciliation.DeadOutboxConfig{Interval: cfg.ReconDeadInterval, Batch: cfg.ReconDeadBatch}),
		reconciliation.StuckAlert(d, reconciliation.StuckAlertConfig{Interval: cfg.ReconStuckInterval, Age: cfg.ReconStuckAge}),
	}
}

// newAPI wires the modules. obs (the metrics) may be nil. bcryptCost 0 means
// identity.DefaultBcryptCost (tests pass a lower cost to stay fast). gateway is the bank (the
// banksim client in serve, a fake in tests).
func newAPI(db *database.DB, cfg config.Wallet, clk clock.Clock, log *slog.Logger, obs observer, bcryptCost int,
	gateway bank.Gateway,
) (*api, error) {
	ledgerSvc := ledger.NewService(db, clk)
	walletSvc := wallet.NewService(db, ledgerSvc, clk)
	identitySvc, err := identity.NewService(db, walletSvc, clk, identity.Config{
		JWTSecret: cfg.JWTSecret, JWTTTL: cfg.JWTTTL, RefreshTTL: cfg.RefreshTokenTTL, BcryptCost: bcryptCost, Log: log,
	})
	if err != nil {
		return nil, err
	}
	transactionSvc := transaction.NewService(clk)
	idempotencySvc := idempotency.NewService(clk, cfg.IdempotencyTTL)
	outboxSvc, err := outbox.NewService(db.Pool(), clk, outbox.Config{
		Workers: cfg.OutboxWorkers, MaxAttempts: cfg.OutboxMaxAttempts, PollInterval: cfg.QueuePollInterval,
	}, log, obs)
	if err != nil {
		return nil, err
	}
	discrepancySvc := discrepancy.NewService(db.Pool(), clk, log)
	paymentSvc := payment.NewService(payment.Deps{
		DB: db, Identity: identitySvc, Wallets: walletSvc, Transactions: transactionSvc, Ledger: ledgerSvc,
		Discrepancies: discrepancySvc, Idempotency: idempotencySvc, Outbox: outboxSvc, Bank: gateway,
		Limits: limits(cfg), Clock: clk, Log: log,
	})
	for eventType, h := range paymentSvc.OutboxHandlers() {
		outboxSvc.Register(eventType, h)
	}
	inboxSvc, err := webhook.NewService(db.Pool(), db, discrepancySvc, clk, webhook.Config{
		Workers: cfg.InboxWorkers, MaxAttempts: cfg.InboxMaxAttempts, PollInterval: cfg.QueuePollInterval,
	}, log, obs)
	if err != nil {
		return nil, err
	}
	inboxSvc.SetHandler(paymentSvc) // bank outcomes are applied by payment (TDR-007 §3)
	transferSvc := transfer.NewService(db, identitySvc, walletSvc, ledgerSvc, transactionSvc, idempotencySvc, limits(cfg), clk)
	a := &api{
		identity: identity.NewHandler(identitySvc, identity.NewLoginLimiter(clk), identity.NewRegisterLimiter(cfg.RegisterLimitPerHour, clk), log),
		wallet:   wallet.NewHandler(walletSvc, log),
		transfer: transfer.NewHandler(transferSvc, log),
		payment:  payment.NewHandler(paymentSvc, log),
		history: transaction.NewHandler(transactionSvc, db.Pool(),
			walletOfUser{db: db, wallets: walletSvc}, parties{db: db, wallets: walletSvc, identity: identitySvc}, log),
		idempotency: idempotencySvc,
		outbox:      outboxSvc,
		inbox:       inboxSvc,
		webhook:     webhook.NewHandler(inboxSvc, []byte(cfg.BankWebhookSecret), clk, log, obs),
	}
	a.internalKey = cfg.InternalAPIKey
	a.operator = operatorRoutes{discrepancies: discrepancy.NewHandler(discrepancySvc, log), wallets: a.wallet}
	a.recon = reconciliation.NewScheduler(db, clk, log, obs)
	for _, j := range reconJobs(reconciliation.Deps{
		DB: db, Transactions: transactionSvc, Payment: paymentSvc, Discrepancies: discrepancySvc,
		Outbox: outboxSvc, Bank: gateway, Clock: clk, Log: log, Stuck: obs,
	}, cfg) {
		a.recon.Register(j)
	}
	return a, nil
}

// limits maps the LIMIT_* configuration to the wallet limits (PRD-001 §5.6).
func limits(cfg config.Wallet) wallet.Limits {
	return wallet.Limits{
		TopUpMin: cfg.LimitTopUpMin, TopUpMax: cfg.LimitTopUpMax,
		WithdrawalMin: cfg.LimitWithdrawalMin, WithdrawalMax: cfg.LimitWithdrawalMax,
		P2PMin: cfg.LimitP2PMin, P2PMax: cfg.LimitP2PMax,
		MaxBalance: cfg.LimitMaxBalance, DailyOutgoing: cfg.LimitDailyOutgoing,
	}
}

// routes mounts every business endpoint.
func (a *api) routes(mux *http.ServeMux) {
	a.identity.Routes(mux)
	a.wallet.Routes(mux, a.identity.RequireAuth)
	a.transfer.Routes(mux, a.identity.RequireAuth)
	a.payment.Routes(mux, a.identity.RequireAuth)
	a.history.Routes(mux, a.identity.RequireAuth)
	a.webhook.Routes(mux) // authenticated by its signature, not a user token
	if a.internalKey != "" {
		requireKey := httpserver.RequireInternalKey(a.internalKey)
		a.operator.discrepancies.InternalRoutes(mux, requireKey)
		a.operator.wallets.InternalRoutes(mux, requireKey)
	}
}

// walletOfUser adapts the wallet module to transaction.WalletOfUser.
type walletOfUser struct {
	db      *database.DB
	wallets *wallet.Service
}

func (a walletOfUser) WalletID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	w, err := a.wallets.GetByUserID(ctx, a.db.Pool(), userID)
	if err != nil {
		return uuid.Nil, err
	}
	return w.ID, nil
}

// parties adapts the wallet and identity modules to transaction.Parties: wallet → owner →
// phone and masked name.
type parties struct {
	db       *database.DB
	wallets  *wallet.Service
	identity *identity.Service
}

func (a parties) ByWallet(ctx context.Context, walletIDs []uuid.UUID) (map[uuid.UUID]transaction.Party, error) {
	out := make(map[uuid.UUID]transaction.Party, len(walletIDs))
	for _, walletID := range walletIDs {
		if _, done := out[walletID]; done {
			continue
		}
		w, err := a.wallets.GetByID(ctx, a.db.Pool(), walletID)
		if err != nil {
			return nil, err
		}
		owner, err := a.identity.GetUser(ctx, w.UserID)
		if err != nil {
			return nil, err
		}
		out[walletID] = transaction.Party{Phone: owner.Phone, MaskedName: identity.MaskName(owner.FullName)}
	}
	return out, nil
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

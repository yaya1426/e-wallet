// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Wallet is the configuration of the wallet service (cmd/wallet).
type Wallet struct {
	DatabaseURL     string        `env:"DATABASE_URL,required,notEmpty"`
	HTTPAddr        string        `env:"HTTP_ADDR"        envDefault:":8080"`
	LogLevel        slog.Level    `env:"-"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"25s"`

	// Unit-of-work settings (TDR-010 §5).
	DBLockTimeout      time.Duration `env:"DB_LOCK_TIMEOUT"      envDefault:"2s"`
	DBStatementTimeout time.Duration `env:"DB_STATEMENT_TIMEOUT" envDefault:"5s"`
	DBTxMaxRetries     int           `env:"DB_TX_MAX_RETRIES"    envDefault:"3"`

	// API idempotency keys (TDR-005 layer 1): how long a key is kept, and how often expired
	// keys are deleted.
	IdempotencyTTL             time.Duration `env:"IDEMPOTENCY_TTL"              envDefault:"24h"`
	IdempotencyCleanupInterval time.Duration `env:"IDEMPOTENCY_CLEANUP_INTERVAL" envDefault:"1h"`

	// Outbox delivery (TDR-006, T012). With OUTBOX_ENABLED=false this instance still
	// enqueues events (an API-only replica) but does not deliver them. QUEUE_POLL_INTERVAL is
	// shared by the outbox and the webhook inbox.
	OutboxEnabled     bool          `env:"OUTBOX_ENABLED"      envDefault:"true"`
	OutboxWorkers     int           `env:"OUTBOX_WORKERS"      envDefault:"8"`
	OutboxMaxAttempts int           `env:"OUTBOX_MAX_ATTEMPTS" envDefault:"10"`
	QueuePollInterval time.Duration `env:"QUEUE_POLL_INTERVAL" envDefault:"500ms"`

	// Bank gateway (TDR-008, T013). BANK_BASE_URL is checked by ValidateServe: the migrate
	// commands run without it.
	BankBaseURL     string        `env:"BANK_BASE_URL"`
	BankHTTPTimeout time.Duration `env:"BANK_HTTP_TIMEOUT" envDefault:"5s"`

	// Bank webhooks (TDR-007, T015). BANK_WEBHOOK_SECRET is the HMAC key shared with the bank,
	// checked by ValidateServe. With INBOX_ENABLED=false this instance still stores webhook
	// events but does not process them.
	BankWebhookSecret string `env:"BANK_WEBHOOK_SECRET"`
	InboxEnabled      bool   `env:"INBOX_ENABLED"      envDefault:"true"`
	InboxWorkers      int    `env:"INBOX_WORKERS"      envDefault:"4"`
	InboxMaxAttempts  int    `env:"INBOX_MAX_ATTEMPTS" envDefault:"10"`

	// Reconciliation (TDR-009, T021): with RECON_ENABLED=false this instance runs no
	// reconciliation job (another instance does).
	ReconEnabled bool `env:"RECON_ENABLED" envDefault:"true"`
	// pending_bank_ops (T022, TDR-009 §2): every PENDING_INTERVAL, ask the bank about up to
	// PENDING_BATCH transactions PROCESSING for more than PENDING_MIN_AGE and not checked in
	// the last PENDING_RECHECK.
	ReconPendingInterval time.Duration `env:"RECON_PENDING_INTERVAL" envDefault:"1m"`
	ReconPendingMinAge   time.Duration `env:"RECON_PENDING_MIN_AGE"  envDefault:"10m"`
	ReconPendingRecheck  time.Duration `env:"RECON_PENDING_RECHECK"  envDefault:"5m"`
	ReconPendingBatch    int           `env:"RECON_PENDING_BATCH"    envDefault:"100"`

	// Money limits in piasters (PRD-001 §5.6, T008); the wallet module applies them.
	LimitTopUpMin      int64 `env:"LIMIT_TOPUP_MIN"       envDefault:"1000"`
	LimitTopUpMax      int64 `env:"LIMIT_TOPUP_MAX"       envDefault:"3000000"`
	LimitWithdrawalMin int64 `env:"LIMIT_WITHDRAWAL_MIN"  envDefault:"1000"`
	LimitWithdrawalMax int64 `env:"LIMIT_WITHDRAWAL_MAX"  envDefault:"3000000"`
	LimitP2PMin        int64 `env:"LIMIT_P2P_MIN"         envDefault:"100"`
	LimitP2PMax        int64 `env:"LIMIT_P2P_MAX"         envDefault:"3000000"`
	LimitMaxBalance    int64 `env:"LIMIT_MAX_BALANCE"     envDefault:"10000000"`
	LimitDailyOutgoing int64 `env:"LIMIT_DAILY_OUTGOING"  envDefault:"6000000"`

	// Access tokens (TDR-011). Only `serve` needs them, so they are checked by ValidateServe:
	// the migrate commands (e.g. the pre-deploy job) run without a JWT secret.
	JWTSecret string        `env:"JWT_SECRET"`
	JWTTTL    time.Duration `env:"JWT_TTL" envDefault:"15m"`
}

// minJWTSecretBytes is the shortest accepted HS256 secret (TDR-011, T004).
const minJWTSecretBytes = 32

// ValidateServe checks the settings that only the HTTP API needs.
func (cfg Wallet) ValidateServe() error {
	var errs []error
	if len(cfg.JWTSecret) < minJWTSecretBytes {
		errs = append(errs, fmt.Errorf("config: JWT_SECRET must be at least %d bytes, got %d", minJWTSecretBytes, len(cfg.JWTSecret)))
	}
	if cfg.JWTTTL <= 0 {
		errs = append(errs, fmt.Errorf("config: JWT_TTL must be positive, got %s", cfg.JWTTTL))
	} else if cfg.JWTTTL%time.Second != 0 {
		// The login response reports expires_in in whole seconds.
		errs = append(errs, fmt.Errorf("config: JWT_TTL must be whole seconds, got %s", cfg.JWTTTL))
	}
	if err := cfg.ValidateBank(); err != nil {
		errs = append(errs, err)
	}
	if len(cfg.BankWebhookSecret) < minWebhookSecretBytes {
		errs = append(errs, fmt.Errorf("config: BANK_WEBHOOK_SECRET must be at least %d bytes, got %d", minWebhookSecretBytes, len(cfg.BankWebhookSecret)))
	}
	return errors.Join(errs...)
}

// ValidateBank checks the bank gateway settings, needed by serve and by the reconciliation
// jobs that ask the bank.
func (cfg Wallet) ValidateBank() error {
	return checkHTTPURL("BANK_BASE_URL", cfg.BankBaseURL)
}

// Banksim is the configuration of the bank simulator (cmd/banksim).
type Banksim struct {
	HTTPAddr string     `env:"BANKSIM_HTTP_ADDR" envDefault:":9090"`
	LogLevel slog.Level `env:"-"`

	// Webhook to the wallet (TDR-007, T014): where to send events, the shared HMAC secret,
	// and how long an operation stays PENDING before it settles.
	WebhookURL    string        `env:"BANKSIM_WEBHOOK_URL"`
	WebhookSecret string        `env:"BANK_WEBHOOK_SECRET"`
	WebhookDelay  time.Duration `env:"BANKSIM_WEBHOOK_DELAY" envDefault:"2s"`
}

// minWebhookSecretBytes is the shortest accepted webhook HMAC secret (T014, T015).
const minWebhookSecretBytes = 32

func (cfg Banksim) validate() error {
	var errs []error
	if err := checkHTTPURL("BANKSIM_WEBHOOK_URL", cfg.WebhookURL); err != nil {
		errs = append(errs, err)
	}
	if len(cfg.WebhookSecret) < minWebhookSecretBytes {
		errs = append(errs, fmt.Errorf("config: BANK_WEBHOOK_SECRET must be at least %d bytes, got %d", minWebhookSecretBytes, len(cfg.WebhookSecret)))
	}
	if cfg.WebhookDelay < 0 {
		errs = append(errs, fmt.Errorf("config: BANKSIM_WEBHOOK_DELAY must not be negative, got %s", cfg.WebhookDelay))
	}
	return errors.Join(errs...)
}

// checkHTTPURL requires an absolute http(s) URL.
func checkHTTPURL(name, raw string) error {
	if raw == "" {
		return fmt.Errorf("config: %s is required", name)
	}
	if u, err := url.Parse(raw); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: %s must be an absolute http(s) URL, got %q", name, raw)
	}
	return nil
}

// logLevel is parsed separately so that only the documented level names are accepted.
type logLevel struct {
	Name string `env:"LOG_LEVEL" envDefault:"info"`
}

// LoadWallet reads the wallet configuration. A nil environ reads the process environment.
func LoadWallet(environ map[string]string) (Wallet, error) {
	var cfg Wallet
	if err := parse(&cfg, environ); err != nil {
		return Wallet{}, err
	}
	if err := cfg.validate(); err != nil {
		return Wallet{}, err
	}
	level, err := loadLogLevel(environ)
	if err != nil {
		return Wallet{}, err
	}
	cfg.LogLevel = level
	return cfg, nil
}

func (cfg Wallet) validate() error {
	var errs []error
	positive := []struct {
		name  string
		value time.Duration
	}{
		{"SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout},
		{"DB_LOCK_TIMEOUT", cfg.DBLockTimeout},
		{"DB_STATEMENT_TIMEOUT", cfg.DBStatementTimeout},
		{"IDEMPOTENCY_TTL", cfg.IdempotencyTTL},
		{"IDEMPOTENCY_CLEANUP_INTERVAL", cfg.IdempotencyCleanupInterval},
		{"QUEUE_POLL_INTERVAL", cfg.QueuePollInterval},
		{"BANK_HTTP_TIMEOUT", cfg.BankHTTPTimeout},
		{"RECON_PENDING_INTERVAL", cfg.ReconPendingInterval},
		{"RECON_PENDING_MIN_AGE", cfg.ReconPendingMinAge},
		{"RECON_PENDING_RECHECK", cfg.ReconPendingRecheck},
	}
	for _, p := range positive {
		if p.value <= 0 {
			errs = append(errs, fmt.Errorf("config: %s must be positive, got %s", p.name, p.value))
		}
	}
	limits := []struct {
		name  string
		value int64
	}{
		{"LIMIT_TOPUP_MIN", cfg.LimitTopUpMin}, {"LIMIT_TOPUP_MAX", cfg.LimitTopUpMax},
		{"LIMIT_WITHDRAWAL_MIN", cfg.LimitWithdrawalMin}, {"LIMIT_WITHDRAWAL_MAX", cfg.LimitWithdrawalMax},
		{"LIMIT_P2P_MIN", cfg.LimitP2PMin}, {"LIMIT_P2P_MAX", cfg.LimitP2PMax},
		{"LIMIT_MAX_BALANCE", cfg.LimitMaxBalance}, {"LIMIT_DAILY_OUTGOING", cfg.LimitDailyOutgoing},
	}
	for _, l := range limits {
		if l.value <= 0 {
			errs = append(errs, fmt.Errorf("config: %s must be positive, got %d", l.name, l.value))
		}
	}
	ranges := []struct {
		minName, maxName string
		lo, hi           int64
	}{
		{"LIMIT_TOPUP_MIN", "LIMIT_TOPUP_MAX", cfg.LimitTopUpMin, cfg.LimitTopUpMax},
		{"LIMIT_WITHDRAWAL_MIN", "LIMIT_WITHDRAWAL_MAX", cfg.LimitWithdrawalMin, cfg.LimitWithdrawalMax},
		{"LIMIT_P2P_MIN", "LIMIT_P2P_MAX", cfg.LimitP2PMin, cfg.LimitP2PMax},
	}
	for _, r := range ranges {
		if r.lo > r.hi {
			errs = append(errs, fmt.Errorf("config: %s (%d) must not exceed %s (%d)", r.minName, r.lo, r.maxName, r.hi))
		}
	}
	counts := []struct {
		name  string
		value int
	}{
		{"OUTBOX_WORKERS", cfg.OutboxWorkers}, {"OUTBOX_MAX_ATTEMPTS", cfg.OutboxMaxAttempts},
		{"INBOX_WORKERS", cfg.InboxWorkers}, {"INBOX_MAX_ATTEMPTS", cfg.InboxMaxAttempts},
	}
	for _, c := range counts {
		if c.value < 1 {
			errs = append(errs, fmt.Errorf("config: %s must be at least 1, got %d", c.name, c.value))
		}
	}
	if cfg.ReconPendingBatch < 1 || cfg.ReconPendingBatch > 1000 {
		errs = append(errs, fmt.Errorf("config: RECON_PENDING_BATCH must be between 1 and 1000, got %d", cfg.ReconPendingBatch))
	}
	if cfg.DBTxMaxRetries < 0 {
		errs = append(errs, fmt.Errorf("config: DB_TX_MAX_RETRIES must not be negative, got %d", cfg.DBTxMaxRetries))
	}
	return errors.Join(errs...)
}

// LoadBanksim reads the bank simulator configuration. A nil environ reads the process environment.
func LoadBanksim(environ map[string]string) (Banksim, error) {
	var cfg Banksim
	if err := parse(&cfg, environ); err != nil {
		return Banksim{}, err
	}
	if err := cfg.validate(); err != nil {
		return Banksim{}, err
	}
	level, err := loadLogLevel(environ)
	if err != nil {
		return Banksim{}, err
	}
	cfg.LogLevel = level
	return cfg, nil
}

func loadLogLevel(environ map[string]string) (slog.Level, error) {
	var raw logLevel
	if err := parse(&raw, environ); err != nil {
		return 0, err
	}
	switch strings.ToLower(raw.Name) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("config: LOG_LEVEL must be one of debug, info, warn, error, got %q", raw.Name)
	}
}

// parse wraps env.ParseWithOptions so that every problem is reported, each naming the
// environment variable involved.
func parse(target any, environ map[string]string) error {
	err := env.ParseWithOptions(target, env.Options{Environment: environ})
	if err == nil {
		return nil
	}
	var aggregate env.AggregateError
	if !errors.As(err, &aggregate) {
		return fmt.Errorf("config: %w", err)
	}
	errs := make([]error, 0, len(aggregate.Errors))
	for _, e := range aggregate.Errors {
		errs = append(errs, describe(target, e))
	}
	return errors.Join(errs...)
}

// describe adds the environment variable name to a field parse error; other errors from
// the env package already name the variable.
func describe(target any, err error) error {
	var parseErr env.ParseError
	if errors.As(err, &parseErr) {
		if name := envName(target, parseErr.Name); name != "" {
			return fmt.Errorf("config: invalid value for %s: %w", name, parseErr.Err)
		}
	}
	return fmt.Errorf("config: %w", err)
}

// envName returns the environment variable name declared in the `env` tag of the named
// field of *target, or "" if there is none.
func envName(target any, field string) string {
	t := reflect.TypeOf(target)
	if t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return ""
	}
	f, ok := t.Elem().FieldByName(field)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(f.Tag.Get("env"), ",")
	return name
}

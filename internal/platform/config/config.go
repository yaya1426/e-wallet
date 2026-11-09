// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
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
	return errors.Join(errs...)
}

// Banksim is the configuration of the bank simulator (cmd/banksim).
type Banksim struct {
	HTTPAddr string     `env:"BANKSIM_HTTP_ADDR" envDefault:":9090"`
	LogLevel slog.Level `env:"-"`
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
	}
	for _, p := range positive {
		if p.value <= 0 {
			errs = append(errs, fmt.Errorf("config: %s must be positive, got %s", p.name, p.value))
		}
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

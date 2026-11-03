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
	if cfg.ShutdownTimeout <= 0 {
		return Wallet{}, fmt.Errorf("config: SHUTDOWN_TIMEOUT must be positive, got %s", cfg.ShutdownTimeout)
	}
	level, err := loadLogLevel(environ)
	if err != nil {
		return Wallet{}, err
	}
	cfg.LogLevel = level
	return cfg, nil
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

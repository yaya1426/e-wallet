package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDatabaseURL = "postgres://u:p@localhost:5432/wallet"

func TestConfig_LoadWallet_Defaults(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL})
	require.NoError(t, err)

	assert.Equal(t, Wallet{
		DatabaseURL:     testDatabaseURL,
		HTTPAddr:        ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 25 * time.Second,

		DBLockTimeout:      2 * time.Second,
		DBStatementTimeout: 5 * time.Second,
		DBTxMaxRetries:     3,

		IdempotencyTTL:             24 * time.Hour,
		IdempotencyCleanupInterval: time.Hour,

		OutboxEnabled: true, OutboxWorkers: 8, OutboxMaxAttempts: 10,
		QueuePollInterval: 500 * time.Millisecond,

		BankHTTPTimeout: 5 * time.Second,

		LimitTopUpMin: 1000, LimitTopUpMax: 3_000_000,
		LimitWithdrawalMin: 1000, LimitWithdrawalMax: 3_000_000,
		LimitP2PMin: 100, LimitP2PMax: 3_000_000,
		LimitMaxBalance: 10_000_000, LimitDailyOutgoing: 6_000_000,

		JWTTTL: 15 * time.Minute,
	}, cfg)
}

func TestConfig_LoadWallet_Overrides(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{
		"DATABASE_URL":     testDatabaseURL,
		"HTTP_ADDR":        ":9999",
		"LOG_LEVEL":        "DEBUG",
		"SHUTDOWN_TIMEOUT": "3s",

		"DB_LOCK_TIMEOUT":      "500ms",
		"DB_STATEMENT_TIMEOUT": "1s",
		"DB_TX_MAX_RETRIES":    "0",
	})
	require.NoError(t, err)

	assert.Equal(t, 500*time.Millisecond, cfg.DBLockTimeout)
	assert.Equal(t, time.Second, cfg.DBStatementTimeout)
	assert.Equal(t, 0, cfg.DBTxMaxRetries)

	assert.Equal(t, ":9999", cfg.HTTPAddr)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
	assert.Equal(t, 3*time.Second, cfg.ShutdownTimeout)
}

func TestConfig_LoadWallet_InvalidValuesNameTheVariable(t *testing.T) {
	tests := []struct {
		name    string
		environ map[string]string
		wantVar string
	}{
		{"missing DATABASE_URL", map[string]string{}, "DATABASE_URL"},
		{"empty DATABASE_URL", map[string]string{"DATABASE_URL": ""}, "DATABASE_URL"},
		{"unknown LOG_LEVEL", map[string]string{"DATABASE_URL": testDatabaseURL, "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{"slog-only LOG_LEVEL syntax", map[string]string{"DATABASE_URL": testDatabaseURL, "LOG_LEVEL": "info+2"}, "LOG_LEVEL"},
		{"unparsable SHUTDOWN_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "SHUTDOWN_TIMEOUT": "soon"}, "SHUTDOWN_TIMEOUT"},
		{"zero SHUTDOWN_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "SHUTDOWN_TIMEOUT": "0s"}, "SHUTDOWN_TIMEOUT"},
		{"negative SHUTDOWN_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "SHUTDOWN_TIMEOUT": "-1s"}, "SHUTDOWN_TIMEOUT"},
		{"zero DB_LOCK_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_LOCK_TIMEOUT": "0s"}, "DB_LOCK_TIMEOUT"},
		{"negative DB_LOCK_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_LOCK_TIMEOUT": "-2s"}, "DB_LOCK_TIMEOUT"},
		{"unparsable DB_LOCK_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_LOCK_TIMEOUT": "two"}, "DB_LOCK_TIMEOUT"},
		{"zero DB_STATEMENT_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_STATEMENT_TIMEOUT": "0"}, "DB_STATEMENT_TIMEOUT"},
		{"unparsable DB_STATEMENT_TIMEOUT", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_STATEMENT_TIMEOUT": "5"}, "DB_STATEMENT_TIMEOUT"},
		{"negative DB_TX_MAX_RETRIES", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_TX_MAX_RETRIES": "-1"}, "DB_TX_MAX_RETRIES"},
		{"non-number DB_TX_MAX_RETRIES", map[string]string{"DATABASE_URL": testDatabaseURL, "DB_TX_MAX_RETRIES": "three"}, "DB_TX_MAX_RETRIES"},
		{"zero LIMIT_P2P_MIN", map[string]string{"DATABASE_URL": testDatabaseURL, "LIMIT_P2P_MIN": "0"}, "LIMIT_P2P_MIN"},
		{"negative LIMIT_MAX_BALANCE", map[string]string{"DATABASE_URL": testDatabaseURL, "LIMIT_MAX_BALANCE": "-1"}, "LIMIT_MAX_BALANCE"},
		{"non-number LIMIT_DAILY_OUTGOING", map[string]string{"DATABASE_URL": testDatabaseURL, "LIMIT_DAILY_OUTGOING": "lots"}, "LIMIT_DAILY_OUTGOING"},
		{"decimal LIMIT_TOPUP_MAX", map[string]string{"DATABASE_URL": testDatabaseURL, "LIMIT_TOPUP_MAX": "30000.50"}, "LIMIT_TOPUP_MAX"},
		{"min above max", map[string]string{"DATABASE_URL": testDatabaseURL, "LIMIT_WITHDRAWAL_MIN": "5000", "LIMIT_WITHDRAWAL_MAX": "4000"}, "LIMIT_WITHDRAWAL_MIN"},
		{"zero IDEMPOTENCY_TTL", map[string]string{"DATABASE_URL": testDatabaseURL, "IDEMPOTENCY_TTL": "0s"}, "IDEMPOTENCY_TTL"},
		{"negative IDEMPOTENCY_TTL", map[string]string{"DATABASE_URL": testDatabaseURL, "IDEMPOTENCY_TTL": "-1h"}, "IDEMPOTENCY_TTL"},
		{"zero IDEMPOTENCY_CLEANUP_INTERVAL", map[string]string{"DATABASE_URL": testDatabaseURL, "IDEMPOTENCY_CLEANUP_INTERVAL": "0s"}, "IDEMPOTENCY_CLEANUP_INTERVAL"},
		{"unparsable IDEMPOTENCY_CLEANUP_INTERVAL", map[string]string{"DATABASE_URL": testDatabaseURL, "IDEMPOTENCY_CLEANUP_INTERVAL": "hourly"}, "IDEMPOTENCY_CLEANUP_INTERVAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadWallet(tt.environ)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantVar)
		})
	}
}

func TestConfig_Outbox(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{
		"DATABASE_URL": testDatabaseURL, "OUTBOX_ENABLED": "false", "OUTBOX_WORKERS": "2",
		"OUTBOX_MAX_ATTEMPTS": "1", "QUEUE_POLL_INTERVAL": "10s",
	})
	require.NoError(t, err)
	assert.False(t, cfg.OutboxEnabled)
	assert.Equal(t, 2, cfg.OutboxWorkers)
	assert.Equal(t, 1, cfg.OutboxMaxAttempts)
	assert.Equal(t, 10*time.Second, cfg.QueuePollInterval)

	invalid := []struct{ name, value string }{
		{"OUTBOX_ENABLED", "maybe"},
		{"OUTBOX_WORKERS", "0"}, {"OUTBOX_WORKERS", "-1"}, {"OUTBOX_WORKERS", "many"},
		{"OUTBOX_MAX_ATTEMPTS", "0"},
		{"QUEUE_POLL_INTERVAL", "0s"}, {"QUEUE_POLL_INTERVAL", "-1s"}, {"QUEUE_POLL_INTERVAL", "500"},
	}
	for _, tt := range invalid {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			_, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, tt.name: tt.value})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.name)
		})
	}
}

func TestConfig_Bank(t *testing.T) {
	serve := map[string]string{"DATABASE_URL": testDatabaseURL, "JWT_SECRET": strings.Repeat("s", 32)}
	with := func(kv ...string) map[string]string {
		env := map[string]string{}
		for k, v := range serve {
			env[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		return env
	}

	cfg, err := LoadWallet(with("BANK_BASE_URL", "http://banksim:9090", "BANK_HTTP_TIMEOUT", "2s"))
	require.NoError(t, err)
	require.NoError(t, cfg.ValidateServe())
	assert.Equal(t, "http://banksim:9090", cfg.BankBaseURL)
	assert.Equal(t, 2*time.Second, cfg.BankHTTPTimeout)

	// The migrate commands only load the config: no bank URL needed.
	cfg, err = LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL})
	require.NoError(t, err)
	assert.ErrorContains(t, cfg.ValidateServe(), "BANK_BASE_URL is required")

	for _, bad := range []string{"banksim:9090", "/v1", "ftp://bank", "http://", "://x"} {
		cfg, err := LoadWallet(with("BANK_BASE_URL", bad))
		require.NoError(t, err)
		assert.ErrorContains(t, cfg.ValidateServe(), "BANK_BASE_URL", bad)
	}
	for _, bad := range []string{"0s", "-1s", "5"} {
		_, err := LoadWallet(with("BANK_BASE_URL", "http://b", "BANK_HTTP_TIMEOUT", bad))
		assert.ErrorContains(t, err, "BANK_HTTP_TIMEOUT", bad)
	}
}

func TestConfig_LoadBanksim_Defaults(t *testing.T) {
	cfg, err := LoadBanksim(map[string]string{})
	require.NoError(t, err)

	assert.Equal(t, Banksim{HTTPAddr: ":9090", LogLevel: slog.LevelInfo}, cfg)
}

func TestConfig_LoadBanksim_InvalidLogLevel(t *testing.T) {
	_, err := LoadBanksim(map[string]string{"LOG_LEVEL": "verbose"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
}

func TestConfig_LoadWallet_ReportsEveryInvalidVariable(t *testing.T) {
	_, err := LoadWallet(map[string]string{"SHUTDOWN_TIMEOUT": "soon"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
	assert.Contains(t, err.Error(), "SHUTDOWN_TIMEOUT")
}

func TestConfig_ValidateServe_JWT(t *testing.T) {
	secret := strings.Repeat("s", 32)
	tests := []struct {
		name    string
		environ map[string]string
		wantVar string // "" means valid
	}{
		{"valid secret, default TTL", map[string]string{"JWT_SECRET": secret}, ""},
		{"missing secret", map[string]string{}, "JWT_SECRET"},
		{"31-byte secret", map[string]string{"JWT_SECRET": secret[:31]}, "JWT_SECRET"},
		{"zero TTL", map[string]string{"JWT_SECRET": secret, "JWT_TTL": "0s"}, "JWT_TTL"},
		{"negative TTL", map[string]string{"JWT_SECRET": secret, "JWT_TTL": "-1m"}, "JWT_TTL"},
		{"sub-second TTL", map[string]string{"JWT_SECRET": secret, "JWT_TTL": "90500ms"}, "JWT_TTL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			environ := map[string]string{"DATABASE_URL": testDatabaseURL, "BANK_BASE_URL": "http://banksim:9090"}
			for k, v := range tt.environ {
				environ[k] = v
			}
			cfg, err := LoadWallet(environ)
			require.NoError(t, err, "the migrate commands load without JWT settings")

			err = cfg.ValidateServe()
			if tt.wantVar == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantVar)
		})
	}
}

func TestConfig_LoadWallet_InvalidJWTTTL(t *testing.T) {
	_, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, "JWT_TTL": "soon"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_TTL")
}

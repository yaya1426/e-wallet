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

		InboxEnabled: true, InboxWorkers: 4, InboxMaxAttempts: 10,

		ReconEnabled: true, ReconPendingInterval: time.Minute, ReconPendingMinAge: 10 * time.Minute,
		ReconPendingRecheck: 5 * time.Minute, ReconPendingBatch: 100,

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
	serve := map[string]string{"DATABASE_URL": testDatabaseURL, "JWT_SECRET": strings.Repeat("s", 32), "BANK_WEBHOOK_SECRET": strings.Repeat("w", 32)}
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

// AC-8 (T015): the inbox settings and the webhook secret.
func TestConfig_Inbox(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL})
	require.NoError(t, err)
	assert.True(t, cfg.InboxEnabled)
	assert.Equal(t, 4, cfg.InboxWorkers)
	assert.Equal(t, 10, cfg.InboxMaxAttempts)
	assert.Empty(t, cfg.BankWebhookSecret)

	cfg, err = LoadWallet(map[string]string{
		"DATABASE_URL": testDatabaseURL, "INBOX_ENABLED": "false", "INBOX_WORKERS": "2", "INBOX_MAX_ATTEMPTS": "1",
	})
	require.NoError(t, err)
	assert.False(t, cfg.InboxEnabled)
	assert.Equal(t, 2, cfg.InboxWorkers)
	assert.Equal(t, 1, cfg.InboxMaxAttempts)

	invalid := []struct{ name, value string }{
		{"INBOX_ENABLED", "maybe"},
		{"INBOX_WORKERS", "0"}, {"INBOX_WORKERS", "-1"}, {"INBOX_WORKERS", "many"},
		{"INBOX_MAX_ATTEMPTS", "0"}, {"INBOX_MAX_ATTEMPTS", "x"},
	}
	for _, tt := range invalid {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			_, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, tt.name: tt.value})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.name)
		})
	}

	// BANK_WEBHOOK_SECRET is required by serve only (the migrate commands run without it).
	base := map[string]string{"DATABASE_URL": testDatabaseURL, "JWT_SECRET": strings.Repeat("s", 32), "BANK_BASE_URL": "http://banksim:9090"}
	for _, tt := range []struct {
		secret string
		ok     bool
	}{{"", false}, {strings.Repeat("w", 31), false}, {strings.Repeat("w", 32), true}} {
		environ := map[string]string{"BANK_WEBHOOK_SECRET": tt.secret}
		for k, v := range base {
			environ[k] = v
		}
		cfg, err := LoadWallet(environ)
		require.NoError(t, err)
		if tt.ok {
			assert.NoError(t, cfg.ValidateServe())
		} else {
			assert.ErrorContains(t, cfg.ValidateServe(), "BANK_WEBHOOK_SECRET must be at least 32 bytes")
		}
	}
}

// T021 AC-7: RECON_ENABLED.
func TestConfig_ReconEnabled(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, "RECON_ENABLED": "false"})
	require.NoError(t, err)
	assert.False(t, cfg.ReconEnabled)
	_, err = LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, "RECON_ENABLED": "sometimes"})
	assert.ErrorContains(t, err, "RECON_ENABLED")
}

// T022 AC-9: the pending_bank_ops settings.
func TestConfig_ReconPending(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, "RECON_PENDING_INTERVAL": "10s",
		"RECON_PENDING_MIN_AGE": "30s", "RECON_PENDING_RECHECK": "20s", "RECON_PENDING_BATCH": "1000"})
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, cfg.ReconPendingInterval)
	assert.Equal(t, 30*time.Second, cfg.ReconPendingMinAge)
	assert.Equal(t, 20*time.Second, cfg.ReconPendingRecheck)
	assert.Equal(t, 1000, cfg.ReconPendingBatch)

	for _, tt := range []struct{ name, value string }{
		{"RECON_PENDING_INTERVAL", "0s"}, {"RECON_PENDING_INTERVAL", "soon"}, {"RECON_PENDING_MIN_AGE", "-1m"},
		{"RECON_PENDING_RECHECK", "0s"}, {"RECON_PENDING_BATCH", "0"}, {"RECON_PENDING_BATCH", "1001"}, {"RECON_PENDING_BATCH", "many"},
	} {
		_, err := LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL, tt.name: tt.value})
		assert.ErrorContains(t, err, tt.name, tt.value)
	}
	cfg, err = LoadWallet(map[string]string{"DATABASE_URL": testDatabaseURL})
	require.NoError(t, err)
	assert.ErrorContains(t, cfg.ValidateBank(), "BANK_BASE_URL is required")
}

// banksimEnv is the smallest valid banksim environment.
func banksimEnv(kv ...string) map[string]string {
	env := map[string]string{"BANKSIM_WEBHOOK_URL": "http://wallet:8080/webhooks/bank", "BANK_WEBHOOK_SECRET": strings.Repeat("w", 32)}
	for i := 0; i < len(kv); i += 2 {
		env[kv[i]] = kv[i+1]
	}
	return env
}

func TestConfig_LoadBanksim_Defaults(t *testing.T) {
	cfg, err := LoadBanksim(banksimEnv())
	require.NoError(t, err)

	assert.Equal(t, Banksim{
		HTTPAddr: ":9090", LogLevel: slog.LevelInfo,
		WebhookURL: "http://wallet:8080/webhooks/bank", WebhookSecret: strings.Repeat("w", 32), WebhookDelay: 2 * time.Second,
	}, cfg)
}

func TestConfig_Banksim(t *testing.T) {
	cfg, err := LoadBanksim(banksimEnv("BANKSIM_WEBHOOK_DELAY", "0s"))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), cfg.WebhookDelay, "0 settles at once")

	tests := []struct {
		name    string
		environ map[string]string
		wantVar string
	}{
		{"no webhook URL", map[string]string{"BANK_WEBHOOK_SECRET": strings.Repeat("w", 32)}, "BANKSIM_WEBHOOK_URL"},
		{"relative webhook URL", banksimEnv("BANKSIM_WEBHOOK_URL", "/webhooks/bank"), "BANKSIM_WEBHOOK_URL"},
		{"no secret", map[string]string{"BANKSIM_WEBHOOK_URL": "http://w/x"}, "BANK_WEBHOOK_SECRET"},
		{"31-byte secret", banksimEnv("BANK_WEBHOOK_SECRET", strings.Repeat("w", 31)), "BANK_WEBHOOK_SECRET"},
		{"negative delay", banksimEnv("BANKSIM_WEBHOOK_DELAY", "-1s"), "BANKSIM_WEBHOOK_DELAY"},
		{"unparsable delay", banksimEnv("BANKSIM_WEBHOOK_DELAY", "soon"), "BANKSIM_WEBHOOK_DELAY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBanksim(tt.environ)
			assert.ErrorContains(t, err, tt.wantVar)
		})
	}
}

func TestConfig_LoadBanksim_InvalidLogLevel(t *testing.T) {
	_, err := LoadBanksim(banksimEnv("LOG_LEVEL", "verbose"))
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
			environ := map[string]string{"DATABASE_URL": testDatabaseURL, "BANK_BASE_URL": "http://banksim:9090", "BANK_WEBHOOK_SECRET": strings.Repeat("w", 32)}
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

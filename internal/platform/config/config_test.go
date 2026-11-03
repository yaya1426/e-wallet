package config

import (
	"log/slog"
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
	}, cfg)
}

func TestConfig_LoadWallet_Overrides(t *testing.T) {
	cfg, err := LoadWallet(map[string]string{
		"DATABASE_URL":     testDatabaseURL,
		"HTTP_ADDR":        ":9999",
		"LOG_LEVEL":        "DEBUG",
		"SHUTDOWN_TIMEOUT": "3s",
	})
	require.NoError(t, err)

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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadWallet(tt.environ)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantVar)
		})
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

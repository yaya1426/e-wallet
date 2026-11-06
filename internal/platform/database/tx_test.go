package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInTx_RetryClassification(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason string
		wantRetry  bool
	}{
		{"serialization failure", &pgconn.PgError{Code: "40001"}, "serialization_failure", true},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, "deadlock", true},
		{"lock timeout", &pgconn.PgError{Code: "55P03"}, "lock_timeout", true},
		{"wrapped deadlock", fmt.Errorf("ledger: post: %w", &pgconn.PgError{Code: "40P01"}), "deadlock", true},
		{"statement timeout", &pgconn.PgError{Code: "57014"}, "", false},
		{"unique violation", &pgconn.PgError{Code: "23505"}, "", false},
		{"context canceled", context.Canceled, "", false},
		{"plain error", errors.New("business rule"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, retry := retryReason(tt.err)
			assert.Equal(t, tt.wantRetry, retry)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestRetryBackoff_Bounds(t *testing.T) {
	for range 1000 {
		d := retryBackoff()
		require.GreaterOrEqual(t, d, 10*time.Millisecond)
		require.LessOrEqual(t, d, 50*time.Millisecond)
	}
}

func TestSleep_ReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := sleep(ctx, time.Hour)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}

func TestTxSettings_Validate(t *testing.T) {
	require.NoError(t, DefaultTxSettings().validate())
	require.NoError(t, TxSettings{LockTimeout: time.Millisecond, StatementTimeout: time.Millisecond}.validate())

	invalid := []TxSettings{
		{LockTimeout: 0, StatementTimeout: time.Second},
		{LockTimeout: time.Second, StatementTimeout: -time.Second},
		{LockTimeout: time.Second, StatementTimeout: time.Second, MaxRetries: -1},
	}
	for _, s := range invalid {
		assert.Error(t, s.validate(), "%+v", s)
	}
	_, err := Open(context.Background(), "postgres://x", Options{})
	assert.ErrorContains(t, err, "invalid tx settings")
}

func TestMilliseconds(t *testing.T) {
	assert.Equal(t, "2000ms", milliseconds(2*time.Second))
	assert.Equal(t, "150ms", milliseconds(150*time.Millisecond))
}

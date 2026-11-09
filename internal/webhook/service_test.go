package webhook_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

type nopHandler struct{}

func (nopHandler) HandleBankEvent(context.Context, pgx.Tx, webhook.BankEvent) error { return nil }

func newTestService(t *testing.T) *webhook.Service {
	t.Helper()
	svc, err := webhook.NewService(&fakeDB{}, nil, clocktest.New(now), webhook.Config{Workers: 1, MaxAttempts: 1, PollInterval: time.Hour}, nil, nil)
	require.NoError(t, err)
	return svc
}

func TestNewService_InvalidConfig(t *testing.T) {
	_, err := webhook.NewService(&fakeDB{}, nil, clocktest.New(now), webhook.Config{Workers: 0, MaxAttempts: 0, PollInterval: time.Second}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook:")
}

func TestService_RunNeedsAHandler(t *testing.T) {
	svc := newTestService(t)
	assert.False(t, svc.HasHandler())
	require.ErrorContains(t, svc.Run(context.Background()), "without a handler")
}

func TestService_SetHandlerWiringBugs(t *testing.T) {
	svc := newTestService(t)
	assert.Panics(t, func() { svc.SetHandler(nil) })

	svc.SetHandler(nopHandler{})
	assert.True(t, svc.HasHandler())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	require.Eventually(t, func() bool { return panics(func() { svc.SetHandler(nopHandler{}) }) },
		5*time.Second, 5*time.Millisecond, "SetHandler after Run panics")
	require.ErrorContains(t, svc.Run(ctx), "called twice")
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func panics(f func()) (did bool) {
	defer func() { did = recover() != nil }()
	f()
	return false
}

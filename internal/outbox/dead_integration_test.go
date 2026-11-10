//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

// T023: dead events are listed for reconciliation and resolved once.
func TestOutbox_ListDeadAndResolve(t *testing.T) {
	f := setup(t)
	svc, err := outbox.NewService(f.db.Pool(), f.clock, outbox.Config{Workers: 1, MaxAttempts: 1, PollInterval: tick}, nil, f.obs)
	require.NoError(t, err)
	failing := func(context.Context, queue.Job) queue.Result { return queue.Retry(errors.New("bank down")) }
	svc.Register(testType, failing)
	svc.Register("other.event", failing)

	first := f.enqueue(t, svc, map[string]string{"n": "1"})
	f.clock.Advance(time.Second)
	second := f.enqueue(t, svc, map[string]string{"n": "2"})
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return svc.Enqueue(ctx, tx, outbox.Event{AggregateType: "transaction", AggregateID: uuid.New(), Type: "other.event", Payload: 1})
	}))
	start(t, svc)
	require.Eventually(t, func() bool {
		var dead int
		require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE status = 'DEAD'`).Scan(&dead))
		return dead == 3
	}, waitFor, tick)

	dead, err := svc.ListDead(context.Background(), f.db.Pool(), []string{testType}, 10)
	require.NoError(t, err)
	require.Len(t, dead, 2, "only the asked types")
	assert.Equal(t, first, dead[0].AggregateID, "oldest first")
	assert.Equal(t, second, dead[1].AggregateID)
	assert.Equal(t, testType, dead[0].Type)
	assert.Equal(t, 1, dead[0].Attempts)
	assert.Contains(t, dead[0].LastError, "bank down")
	limited, err := svc.ListDead(context.Background(), f.db.Pool(), []string{testType}, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)

	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return svc.Resolve(ctx, tx, dead[0].ID, "bank has no record")
	}))
	r := f.row(t, first)
	assert.Equal(t, "DONE", r.Status)
	require.NotNil(t, r.LastError)
	assert.Equal(t, "resolved by reconciliation: bank has no record", *r.LastError)
	assert.NotNil(t, r.ProcessedAt)

	err = svc.Resolve(context.Background(), f.db.Pool(), dead[0].ID, "again")
	assert.ErrorIs(t, err, outbox.ErrNotDead, "resolved once")
	rest, err := svc.ListDead(context.Background(), f.db.Pool(), []string{testType}, 10)
	require.NoError(t, err)
	require.Len(t, rest, 1)
	assert.Equal(t, second, rest[0].AggregateID)

	// A resolve whose transaction rolls back leaves the event DEAD.
	errRollback := errors.New("business change failed")
	err = f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		require.NoError(t, svc.Resolve(ctx, tx, rest[0].ID, "x"))
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	assert.Equal(t, "DEAD", f.row(t, second).Status)
}

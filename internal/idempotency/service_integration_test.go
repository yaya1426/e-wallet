//go:build integration

package idempotency_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
)

const ttl = 24 * time.Hour

// retryCounter is a database.TxObserver that counts InTx retries.
type retryCounter struct{ retries, tryAgain atomic.Int64 }

func (r *retryCounter) ObserveTxRetry(string) { r.retries.Add(1) }
func (r *retryCounter) ObserveTxTryAgain()    { r.tryAgain.Add(1) }

type fixture struct {
	db      *database.DB
	svc     *idempotency.Service
	clock   *clocktest.Fake
	user    uuid.UUID
	retries *retryCounter
}

func setup(t *testing.T, lockTimeout time.Duration) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	settings := database.DefaultTxSettings()
	settings.LockTimeout = lockTimeout
	counter := &retryCounter{}
	db, err := database.Open(ctx, url, database.Options{Tx: settings, Observer: counter})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))

	user := uuid.New()
	_, err = db.Pool().Exec(ctx, `INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
		VALUES ($1, '+201000000001', 'Test User', 'x', now())`, user)
	require.NoError(t, err)

	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	return fixture{db: db, svc: idempotency.NewService(clk, ttl), clock: clk, user: user, retries: counter}
}

func (f fixture) request(key string, hash byte) idempotency.Request {
	return idempotency.Request{UserID: f.user, Key: key, Method: http.MethodPost, Path: "/v1/transfers", Hash: []byte{hash}}
}

// do runs one idempotent request: Begin, then (when new) Complete with status/body. A
// non-nil fail makes the transaction roll back after Begin.
func (f fixture) do(ctx context.Context, req idempotency.Request, status int, body any, fail error) (*idempotency.Replay, error) {
	var replay *idempotency.Replay
	err := f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if replay, err = f.svc.Begin(ctx, tx, req); err != nil || replay != nil {
			return err
		}
		if fail != nil {
			return fail
		}
		return f.svc.Complete(ctx, tx, req.UserID, req.Key, status, body, nil)
	})
	return replay, err
}

func TestBegin_FirstThenReplay(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	req := f.request(uuid.NewString(), 1)

	replay, err := f.do(ctx, req, http.StatusCreated, map[string]any{"transaction_id": "t-1", "status": "COMPLETED"}, nil)
	require.NoError(t, err)
	assert.Nil(t, replay, "the first request does the work")

	replay, err = f.do(ctx, req, 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.Equal(t, http.StatusCreated, replay.Status)
	assert.JSONEq(t, `{"transaction_id":"t-1","status":"COMPLETED"}`, string(replay.Body))

	rec := httptest.NewRecorder()
	idempotency.WriteReplay(rec, *replay)
	assert.Equal(t, "true", rec.Header().Get(idempotency.HeaderReplayed))
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestBegin_Reused(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	key := uuid.NewString()
	_, err := f.do(ctx, f.request(key, 1), http.StatusCreated, map[string]string{}, nil)
	require.NoError(t, err)

	_, err = f.do(ctx, f.request(key, 2), 0, nil, nil)
	assert.ErrorIs(t, err, idempotency.ErrKeyReused, "different hash")

	otherPath := f.request(key, 1)
	otherPath.Path = "/v1/withdrawals"
	_, err = f.do(ctx, otherPath, 0, nil, nil)
	assert.ErrorIs(t, err, idempotency.ErrKeyReused, "same hash, different path")
}

func TestBegin_Replay4xx(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	req := f.request(uuid.NewString(), 1)
	errBody := map[string]any{"error": map[string]string{"code": "INSUFFICIENT_FUNDS", "message": "Available balance is too low."}}
	_, err := f.do(ctx, req, http.StatusUnprocessableEntity, errBody, nil)
	require.NoError(t, err)

	replay, err := f.do(ctx, req, 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.Equal(t, http.StatusUnprocessableEntity, replay.Status)
	assert.Contains(t, string(replay.Body), "INSUFFICIENT_FUNDS")
}

func TestBegin_AfterRollback(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	req := f.request(uuid.NewString(), 1)
	boom := errors.New("business work failed with a 5xx")

	_, err := f.do(ctx, req, 0, nil, boom)
	require.ErrorIs(t, err, boom)

	replay, err := f.do(ctx, req, http.StatusCreated, map[string]string{}, nil)
	require.NoError(t, err)
	assert.Nil(t, replay, "the rolled-back key is gone; the retry starts fresh")
}

func TestComplete_WithoutBegin(t *testing.T) {
	f := setup(t, 2*time.Second)
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.svc.Complete(ctx, tx, f.user, uuid.NewString(), 201, map[string]string{}, nil)
	})
	assert.ErrorIs(t, err, idempotency.ErrNotBegun)
}

// holdFirst starts request 1, which begins the key and keeps its transaction open for hold
// before completing. It returns once the key insert is done, and a channel with the result.
func (f fixture) holdFirst(t *testing.T, req idempotency.Request, hold time.Duration) <-chan error {
	t.Helper()
	begun := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := f.svc.Begin(ctx, tx, req); err != nil {
				return err
			}
			close(begun)
			time.Sleep(hold)
			return f.svc.Complete(ctx, tx, req.UserID, req.Key, http.StatusCreated, map[string]string{"by": "first"}, nil)
		})
	}()
	select {
	case <-begun:
	case err := <-done:
		t.Fatalf("request 1 failed: %v", err)
	}
	return done
}

func TestBegin_ConcurrentSameKey_InProgress(t *testing.T) {
	f := setup(t, 200*time.Millisecond)
	req := f.request(uuid.NewString(), 1)
	first := f.holdFirst(t, req, time.Second) // longer than the 200 ms lock timeout

	attempts := 0
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		attempts++
		_, err := f.svc.Begin(ctx, tx, req)
		return err
	})

	assert.ErrorIs(t, err, idempotency.ErrRequestInProgress)
	assert.Equal(t, 1, attempts, "InTx must not retry a key that is in progress")
	assert.Zero(t, f.retries.retries.Load(), "no db_tx_retries_total increment")
	require.NoError(t, <-first)
}

func TestBegin_ConcurrentSameKey_Replay(t *testing.T) {
	f := setup(t, 2*time.Second)
	req := f.request(uuid.NewString(), 1)
	first := f.holdFirst(t, req, 200*time.Millisecond) // commits within the lock timeout

	replay, err := f.do(context.Background(), req, 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, replay, "request 2 waited for request 1 and replays its answer")
	assert.JSONEq(t, `{"by":"first"}`, string(replay.Body))
	require.NoError(t, <-first)
}

func TestBegin_ExpiredRowReplaced(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	key := uuid.NewString()
	_, err := f.do(ctx, f.request(key, 1), http.StatusCreated, map[string]string{"n": "1"}, nil)
	require.NoError(t, err)

	f.clock.Advance(ttl + time.Second) // expired, not yet cleaned up
	replay, err := f.do(ctx, f.request(key, 2), http.StatusCreated, map[string]string{"n": "2"}, nil)
	require.NoError(t, err)
	assert.Nil(t, replay, "an expired key counts as absent, even with a different hash")

	replay, err = f.do(ctx, f.request(key, 2), 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.JSONEq(t, `{"n":"2"}`, string(replay.Body))
}

func TestCleanup_DeletesExpired(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx := context.Background()
	for i := range 5 {
		_, err := f.do(ctx, f.request(uuid.NewString(), byte(i)), 201, map[string]string{}, nil)
		require.NoError(t, err)
	}
	f.clock.Advance(ttl + time.Minute)
	live := uuid.NewString()
	_, err := f.do(ctx, f.request(live, 9), 201, map[string]string{}, nil)
	require.NoError(t, err)

	deleted, err := f.svc.DeleteExpired(ctx, f.db.Pool(), 2) // batches of 2: 2 + 2 + 1
	require.NoError(t, err)
	assert.Equal(t, int64(5), deleted)

	var keys []string
	rows, err := f.db.Pool().Query(ctx, `SELECT key FROM idempotency_keys`)
	require.NoError(t, err)
	keys, err = pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{live}, keys, "live keys are kept")

	// The cleanup job's single run uses the same path.
	f.clock.Advance(ttl + time.Minute)
	idempotency.NewCleanup(f.svc, f.db.Pool(), time.Hour, nil).RunOnce(ctx)
	var left int
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM idempotency_keys`).Scan(&left))
	assert.Zero(t, left)
}

func TestCleanup_RunStopsOnCancel(t *testing.T) {
	f := setup(t, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- idempotency.NewCleanup(f.svc, f.db.Pool(), time.Hour, nil).Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not stop")
	}
}

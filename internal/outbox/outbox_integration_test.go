//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

const (
	testType = "test.event"
	waitFor  = 10 * time.Second
	tick     = 10 * time.Millisecond
	// noPoll keeps a runner from polling on its own: it claims at start and on Wake only.
	noPoll = time.Hour
)

// observer records the outbox metrics.
type observer struct {
	mu        sync.Mutex
	pending   int64
	oldestAge time.Duration
	dead      int
}

func (o *observer) ObserveQueueClaimed(string, int)               {}
func (o *observer) ObserveQueueJob(string, string, time.Duration) {}
func (o *observer) ObserveOutboxBacklog(pending int64, oldestAge time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending, o.oldestAge = pending, oldestAge
}

func (o *observer) ObserveOutboxDead() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dead++
}

func (o *observer) snapshot() (int64, time.Duration, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pending, o.oldestAge, o.dead
}

type fixture struct {
	db    *database.DB
	clock *clocktest.Fake
	obs   *observer
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))
	return fixture{db: db, clock: clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)), obs: &observer{}}
}

// service returns an outbox (one "instance") with h registered for testType.
func (f fixture) service(t *testing.T, workers int, poll time.Duration, h outbox.Handler) *outbox.Service {
	t.Helper()
	svc, err := outbox.NewService(f.db.Pool(), f.clock, outbox.Config{Workers: workers, MaxAttempts: 3, PollInterval: poll}, nil, f.obs)
	require.NoError(t, err)
	svc.Register(testType, h)
	return svc
}

// start runs svc until the test ends (or stop is called) and waits for Run to return.
func start(t *testing.T, svc *outbox.Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(waitFor):
				t.Error("outbox Run did not return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// enqueue enqueues one event in its own committed transaction and returns its aggregate ID.
func (f fixture) enqueue(t *testing.T, svc *outbox.Service, payload any) uuid.UUID {
	t.Helper()
	agg := uuid.New()
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return svc.Enqueue(ctx, tx, outbox.Event{AggregateType: "transaction", AggregateID: agg, Type: testType, Payload: payload})
	})
	require.NoError(t, err)
	return agg
}

type eventRow struct {
	ID            uuid.UUID
	Status        string
	Attempts      int
	Payload       string
	LastError     *string
	NextAttemptAt time.Time
	LockedBy      *string
	ProcessedAt   *time.Time
}

func (f fixture) row(t *testing.T, aggregateID uuid.UUID) eventRow {
	t.Helper()
	var r eventRow
	err := f.db.Pool().QueryRow(context.Background(), `SELECT id, status, attempts, payload::text, last_error,
		next_attempt_at, locked_by, processed_at FROM outbox_events WHERE aggregate_id = $1`, aggregateID).
		Scan(&r.ID, &r.Status, &r.Attempts, &r.Payload, &r.LastError, &r.NextAttemptAt, &r.LockedBy, &r.ProcessedAt)
	require.NoError(t, err)
	return r
}

func (f fixture) waitRow(t *testing.T, aggregateID uuid.UUID, cond func(eventRow) bool) eventRow {
	t.Helper()
	var last eventRow
	require.Eventually(t, func() bool {
		last = f.row(t, aggregateID)
		return cond(last)
	}, waitFor, tick, "event row never reached the expected state")
	return last
}

func (f fixture) count(t *testing.T, where string) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE `+where).Scan(&n))
	return n
}

func TestOutbox_CommitDelivers_RollbackDoesNot(t *testing.T) {
	f := setup(t)
	delivered := make(chan queue.Job, 10)
	svc := f.service(t, 2, 20*time.Millisecond, func(_ context.Context, job queue.Job) queue.Result {
		delivered <- job
		return queue.Done()
	})
	errRollback := errors.New("business step failed")
	rolledBack := uuid.New()
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		require.NoError(t, svc.Enqueue(ctx, tx, outbox.Event{AggregateType: "transaction", AggregateID: rolledBack, Type: testType, Payload: "never"}))
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	committed := f.enqueue(t, svc, map[string]any{"amount": 1500, "note": "مرحبا"})

	start(t, svc)

	var job queue.Job
	select {
	case job = <-delivered:
	case <-time.After(waitFor):
		t.Fatal("committed event was not delivered")
	}
	assert.Equal(t, testType, job.Type)
	assert.JSONEq(t, `{"amount":1500,"note":"مرحبا"}`, string(job.Payload))
	assert.Equal(t, 1, job.Attempts)
	r := f.waitRow(t, committed, func(r eventRow) bool { return r.Status == "DONE" })
	assert.Equal(t, job.ID, r.ID)
	require.NotNil(t, r.ProcessedAt)
	assert.True(t, r.ProcessedAt.Equal(f.clock.Now()))
	assert.Nil(t, r.LastError)
	assert.Nil(t, r.LockedBy)

	time.Sleep(200 * time.Millisecond) // ~10 more polls
	assert.Empty(t, delivered, "delivered once")
	assert.Equal(t, 0, f.count(t, fmt.Sprintf("aggregate_id = '%s'", rolledBack)), "a rolled-back event does not exist")
}

func TestOutbox_TwoRunnersExactlyOnce(t *testing.T) {
	f := setup(t)
	var (
		mu    sync.Mutex
		calls = map[uuid.UUID]int{}
		total int
	)
	handler := func(_ context.Context, job queue.Job) queue.Result {
		mu.Lock()
		calls[job.ID]++
		total++
		mu.Unlock()
		time.Sleep(time.Millisecond) // let the runners interleave
		return queue.Done()
	}
	a := f.service(t, 4, 5*time.Millisecond, handler)
	b := f.service(t, 4, 5*time.Millisecond, handler)
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		for i := range 100 {
			if err := a.Enqueue(ctx, tx, outbox.Event{AggregateType: "transaction", AggregateID: uuid.New(), Type: testType, Payload: i}); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	start(t, a)
	start(t, b)

	require.Eventually(t, func() bool { return f.count(t, "status = 'DONE'") == 100 }, waitFor, tick)
	time.Sleep(100 * time.Millisecond) // a double delivery would have shown up by now
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, calls, 100)
	assert.Equal(t, 100, total)
	for id, n := range calls {
		assert.Equal(t, 1, n, "event %s handled %d times", id, n)
	}
	assert.Equal(t, 100, f.count(t, "attempts = 1"), "no event was claimed twice")
}

func TestOutbox_ExpiredLeaseReclaimed(t *testing.T) {
	f := setup(t)
	started, release := make(chan struct{}), make(chan struct{})
	// Instance A claims the event, then "crashes": its handler hangs and it never marks the
	// event while it holds the lease. It never polls again (noPoll), like a dead process.
	crashed := f.service(t, 1, noPoll, func(context.Context, queue.Job) queue.Result {
		close(started)
		<-release
		return queue.Done()
	})
	var handledByB []queue.Job
	var mu sync.Mutex
	healthy := f.service(t, 1, noPoll, func(_ context.Context, job queue.Job) queue.Result {
		mu.Lock()
		defer mu.Unlock()
		handledByB = append(handledByB, job)
		return queue.Done()
	})
	agg := f.enqueue(t, crashed, "payload")
	stopA := start(t, crashed)
	<-started
	r := f.row(t, agg)
	require.Equal(t, "PROCESSING", r.Status)
	require.Equal(t, 1, r.Attempts)

	start(t, healthy) // claims at start: the lease is still valid, so nothing is taken
	healthy.Wake()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, f.row(t, agg).Attempts, "a valid lease is not taken over")

	f.clock.Advance(outbox.DefaultLease + time.Second)
	healthy.Wake()

	r = f.waitRow(t, agg, func(r eventRow) bool { return r.Status == "DONE" })
	assert.Equal(t, 2, r.Attempts)
	mu.Lock()
	require.Len(t, handledByB, 1)
	assert.Equal(t, 2, handledByB[0].Attempts)
	mu.Unlock()

	// A's late mark is stale (attempts 1 ≠ 2) and changes nothing.
	f.clock.Advance(time.Minute)
	close(release)
	stopA() // returns after A's handler finished and its mark ran
	after := f.row(t, agg)
	assert.Equal(t, r.ProcessedAt, after.ProcessedAt)
	assert.Equal(t, 2, after.Attempts)
}

func TestOutbox_RetryThenDead(t *testing.T) {
	f := setup(t)
	svc := f.service(t, 1, noPoll, func(context.Context, queue.Job) queue.Result {
		return queue.Retry(errors.New("bank unavailable"))
	})
	agg := f.enqueue(t, svc, "payload")
	start(t, svc)

	r := f.waitRow(t, agg, func(r eventRow) bool { return r.Status == "PENDING" && r.Attempts == 1 })
	assert.True(t, r.NextAttemptAt.After(f.clock.Now()), "next attempt is in the future")
	assert.False(t, r.NextAttemptAt.After(f.clock.Now().Add(2*time.Second)), "backoff of attempt 1 is below 2 s")
	require.NotNil(t, r.LastError)
	assert.Equal(t, "bank unavailable", *r.LastError)
	assert.Nil(t, r.LockedBy)

	svc.Wake() // not due yet: nothing happens
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, f.row(t, agg).Attempts)

	f.clock.Advance(10 * time.Minute)
	svc.Wake()
	f.waitRow(t, agg, func(r eventRow) bool { return r.Status == "PENDING" && r.Attempts == 2 })
	_, _, dead := f.obs.snapshot()
	assert.Equal(t, 0, dead)

	f.clock.Advance(10 * time.Minute)
	svc.Wake()
	r = f.waitRow(t, agg, func(r eventRow) bool { return r.Status == "DEAD" })
	assert.Equal(t, 3, r.Attempts, "OUTBOX_MAX_ATTEMPTS")
	require.NotNil(t, r.LastError)
	assert.Equal(t, "bank unavailable", *r.LastError)
	assert.Nil(t, r.ProcessedAt)
	_, _, dead = f.obs.snapshot()
	assert.Equal(t, 1, dead, "outbox_dead_total + 1")
}

func TestOutbox_FailEndsDone(t *testing.T) {
	f := setup(t)
	svc := f.service(t, 1, 20*time.Millisecond, func(context.Context, queue.Job) queue.Result {
		return queue.Fail("INVALID_ACCOUNT")
	})
	agg := f.enqueue(t, svc, "payload")
	start(t, svc)

	r := f.waitRow(t, agg, func(r eventRow) bool { return r.Status != "PENDING" && r.Status != "PROCESSING" })
	assert.Equal(t, "DONE", r.Status)
	require.NotNil(t, r.LastError)
	assert.Equal(t, "INVALID_ACCOUNT", *r.LastError)
	assert.NotNil(t, r.ProcessedAt)
	assert.Equal(t, 1, r.Attempts)
}

func TestOutbox_WakeAfterCommit(t *testing.T) {
	f := setup(t)
	delivered := make(chan uuid.UUID, 10)
	svc := f.service(t, 1, 10*time.Second, func(_ context.Context, job queue.Job) queue.Result {
		delivered <- job.ID
		return queue.Done()
	})
	waitDelivered := func(within time.Duration) bool {
		select {
		case <-delivered:
			return true
		case <-time.After(within):
			return false
		}
	}
	// The first event is claimed at start; after it the dispatcher waits for the 10 s poll.
	f.enqueue(t, svc, "first")
	start(t, svc)
	require.True(t, waitDelivered(waitFor))

	f.enqueue(t, svc, "without wake")
	assert.False(t, waitDelivered(time.Second), "without Wake the event waits for the 10 s poll")

	f.enqueue(t, svc, "with wake")
	begin := time.Now()
	svc.Wake()
	require.True(t, waitDelivered(2*time.Second))
	require.True(t, waitDelivered(2*time.Second), "the claim after Wake takes both due events")
	assert.Less(t, time.Since(begin), 2*time.Second)
}

func TestOutbox_UnknownTypeIsRetried(t *testing.T) {
	f := setup(t)
	svc := f.service(t, 1, noPoll, func(context.Context, queue.Job) queue.Result { return queue.Done() })
	agg := uuid.New()
	// Enqueued by an instance that knows the type (e.g. a newer version).
	_, err := f.db.Pool().Exec(context.Background(), `INSERT INTO outbox_events
		(id, aggregate_type, aggregate_id, event_type, payload, status, next_attempt_at, created_at)
		VALUES ($1, 'transaction', $2, 'newer.type', '{}', 'PENDING', $3, $3)`, uuid.New(), agg, f.clock.Now())
	require.NoError(t, err)

	start(t, svc)

	r := f.waitRow(t, agg, func(r eventRow) bool { return r.Status == "PENDING" && r.Attempts == 1 })
	require.NotNil(t, r.LastError)
	assert.Contains(t, *r.LastError, `unknown event type: "newer.type"`)
}

func TestOutbox_Gauges(t *testing.T) {
	f := setup(t)
	// Nanoseconds, which PostgreSQL rounds away: an empty backlog must still report age 0.
	f.clock.Advance(1234 * time.Nanosecond)
	svc := f.service(t, 1, noPoll, func(context.Context, queue.Job) queue.Result { return queue.Done() })
	ctx := context.Background()

	require.NoError(t, svc.RefreshBacklog(ctx))
	pending, age, _ := f.obs.snapshot()
	assert.Equal(t, int64(0), pending)
	assert.Equal(t, time.Duration(0), age)

	oldest := f.enqueue(t, svc, 1)
	f.clock.Advance(30 * time.Second)
	middle := f.enqueue(t, svc, 2)
	f.clock.Advance(30 * time.Second)
	f.enqueue(t, svc, 3)
	f.clock.Advance(30 * time.Second)
	_, err := f.db.Pool().Exec(ctx, `UPDATE outbox_events SET status = 'PROCESSING' WHERE aggregate_id = $1`, middle)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshBacklog(ctx))
	pending, age, _ = f.obs.snapshot()
	assert.Equal(t, int64(3), pending, "PENDING and PROCESSING count")
	assert.InDelta(t, 90*time.Second, age, float64(time.Microsecond))

	_, err = f.db.Pool().Exec(ctx, `UPDATE outbox_events SET status = 'DONE' WHERE aggregate_id = $1`, oldest)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshBacklog(ctx))
	pending, age, _ = f.obs.snapshot()
	assert.Equal(t, int64(2), pending, "DONE is not counted")
	assert.InDelta(t, 60*time.Second, age, float64(time.Microsecond))
}

func TestOutbox_RunTwiceAndRegisterAfterRun(t *testing.T) {
	f := setup(t)
	delivered := make(chan struct{}, 1)
	svc := f.service(t, 1, noPoll, func(context.Context, queue.Job) queue.Result {
		delivered <- struct{}{}
		return queue.Done()
	})
	f.enqueue(t, svc, "x")
	start(t, svc)
	select { // a delivery proves Run has started
	case <-delivered:
	case <-time.After(waitFor):
		t.Fatal("not delivered")
	}

	assert.PanicsWithValue(t, `outbox: Register("late.type") after Run`, func() {
		svc.Register("late.type", func(context.Context, queue.Job) queue.Result { return queue.Done() })
	})
	assert.EqualError(t, svc.Run(context.Background()), "outbox: Run called twice")
}

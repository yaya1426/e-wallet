package queue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/platform/queue/queuetest"
)

func config(workers int) queue.Config {
	return queue.Config{Name: "test", Workers: workers, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: time.Second, MaxAttempts: 3}
}

type countingObserver struct {
	mu      sync.Mutex
	claimed int
	results map[string]int
}

func (o *countingObserver) ObserveQueueClaimed(_ string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.claimed += n
}

func (o *countingObserver) ObserveQueueJob(_, result string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.results[result]++
}

func (o *countingObserver) get(result string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.results[result]
}

// start runs the runner in the background and returns a stop function that cancels it and
// waits for Run to return.
func start(t *testing.T, r *queue.Runner) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("runner did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestRunner_ProcessesAllOnce_RespectsWorkers(t *testing.T) {
	store := queuetest.New(clock.System{})
	for range 10 {
		store.Add("job", nil)
	}
	var active, maxActive atomic.Int32
	var mu sync.Mutex
	handled := map[uuid.UUID]int{}
	obs := &countingObserver{results: map[string]int{}}
	r, err := queue.NewRunner(config(3), store, func(_ context.Context, job queue.Job) queue.Result {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		mu.Lock()
		handled[job.ID]++
		mu.Unlock()
		return queue.Done()
	}, clock.System{}, nil, obs)
	require.NoError(t, err)
	start(t, r)

	require.Eventually(t, func() bool { return store.Count(queuetest.StatusDone) == 10 }, 5*time.Second, 5*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, handled, 10)
	for id, n := range handled {
		assert.Equal(t, 1, n, "job %s handled once", id)
	}
	assert.LessOrEqual(t, maxActive.Load(), int32(3), "never more than 3 handlers at once")
	assert.Equal(t, 10, obs.get("done"))
}

func TestRunner_RetryThenDead(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	store := queuetest.New(clk)
	id := store.Add("job", nil)
	obs := &countingObserver{results: map[string]int{}}
	r, err := queue.NewRunner(config(1), store, func(context.Context, queue.Job) queue.Result {
		return queue.Retry(errors.New("bank timeout"))
	}, clk, nil, obs)
	require.NoError(t, err)
	queue.SetJitter(r, func(n int64) int64 { return n - 1 }) // the largest allowed backoff
	start(t, r)

	waitAttempts := func(n int) {
		require.Eventually(t, func() bool { return len(store.Get(id).Retries) >= n || store.Get(id).Status == queuetest.StatusDead },
			5*time.Second, 5*time.Millisecond)
	}
	waitAttempts(1)
	rec := store.Get(id)
	assert.Equal(t, queuetest.StatusPending, rec.Status)
	assert.Equal(t, clk.Now().Add(2*time.Second-1), rec.Retries[0], "attempt 1: backoff < 1s·2^1")
	assert.Equal(t, "bank timeout", rec.LastError)

	clk.Advance(2 * time.Second)
	r.Wake()
	waitAttempts(2)
	assert.Equal(t, clk.Now().Add(4*time.Second-1), store.Get(id).Retries[1], "attempt 2: backoff < 1s·2^2")

	clk.Advance(4 * time.Second)
	r.Wake()
	require.Eventually(t, func() bool { return store.Get(id).Status == queuetest.StatusDead }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 3, store.Get(id).Job.Attempts, "dead once attempts reach MaxAttempts")
	assert.Equal(t, 2, obs.get("retry"))
	assert.Equal(t, 1, obs.get("dead"))
}

func TestRunner_FailAndPanic(t *testing.T) {
	store := queuetest.New(clock.System{})
	failing := store.Add("fail", nil)
	panicking := store.Add("panic", nil)
	ok := store.Add("ok", nil)
	r, err := queue.NewRunner(config(1), store, func(_ context.Context, job queue.Job) queue.Result {
		switch job.Type {
		case "fail":
			return queue.Fail("bank rejected: ACCOUNT_CLOSED")
		case "panic":
			panic("boom")
		default:
			return queue.Done()
		}
	}, clock.System{}, nil, nil)
	require.NoError(t, err)
	start(t, r)

	require.Eventually(t, func() bool { return store.Get(ok).Status == queuetest.StatusDone }, 5*time.Second, 5*time.Millisecond,
		"the runner keeps working after a panic")
	assert.Equal(t, queuetest.StatusFailed, store.Get(failing).Status)
	assert.Equal(t, "bank rejected: ACCOUNT_CLOSED", store.Get(failing).LastError)
	p := store.Get(panicking)
	assert.Equal(t, queuetest.StatusPending, p.Status, "a panic is a retry")
	assert.Contains(t, p.LastError, "boom")
	assert.Len(t, p.Retries, 1)
}

func TestRunner_HandlerTimeout(t *testing.T) {
	store := queuetest.New(clock.System{})
	store.Add("slow", nil)
	cfg := config(1)
	cfg.HandlerTimeout = 50 * time.Millisecond
	seen := make(chan error, 1)
	r, err := queue.NewRunner(cfg, store, func(ctx context.Context, _ queue.Job) queue.Result {
		<-ctx.Done()
		seen <- ctx.Err()
		return queue.Retry(ctx.Err())
	}, clock.System{}, nil, nil)
	require.NoError(t, err)
	start(t, r)

	select {
	case err := <-seen:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("handler ctx was not canceled")
	}
}

func TestRunner_WakeClaimsEarly(t *testing.T) {
	store := queuetest.New(clock.System{})
	cfg := config(1)
	cfg.PollInterval = time.Hour // only Wake can trigger a claim after the first one
	r, err := queue.NewRunner(cfg, store, func(context.Context, queue.Job) queue.Result { return queue.Done() }, clock.System{}, nil, nil)
	require.NoError(t, err)
	start(t, r)
	require.Eventually(t, func() bool { return store.Claims() >= 1 }, time.Second, time.Millisecond, "initial claim")

	id := store.Add("job", nil)
	began := time.Now()
	for range 1000 {
		r.Wake() // never blocks
	}
	assert.Less(t, time.Since(began), time.Second)
	require.Eventually(t, func() bool { return store.Get(id).Status == queuetest.StatusDone }, 2*time.Second, time.Millisecond,
		"claimed long before the next poll tick")
}

func TestRunner_GracefulShutdown(t *testing.T) {
	store := queuetest.New(clock.System{})
	store.Add("block", nil)
	store.Add("block", nil)
	release := make(chan struct{})
	var running, finished atomic.Int32
	r, err := queue.NewRunner(config(2), store, func(ctx context.Context, _ queue.Job) queue.Result {
		running.Add(1)
		<-release
		if ctx.Err() != nil {
			return queue.Retry(ctx.Err())
		}
		finished.Add(1)
		return queue.Done()
	}, clock.System{}, nil, nil)
	require.NoError(t, err)
	stop := start(t, r)
	require.Eventually(t, func() bool { return running.Load() == 2 }, 5*time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	time.Sleep(50 * time.Millisecond) // the root ctx is now canceled
	claimsAtCancel := store.Claims()
	late := store.Add("late", nil)
	select {
	case <-stopped:
		t.Fatal("Run returned before the running handlers finished")
	default:
	}
	close(release)
	<-stopped

	assert.Equal(t, int32(2), finished.Load(), "running handlers finish with a live ctx")
	assert.Equal(t, 2, store.Count(queuetest.StatusDone))
	assert.Equal(t, claimsAtCancel, store.Claims(), "no claim after cancel")
	assert.Equal(t, queuetest.StatusPending, store.Get(late).Status)
}

func TestBackoff(t *testing.T) {
	largest := func(n int64) int64 { return n - 1 }
	zero := func(int64) int64 { return 0 }
	assert.Equal(t, 2*time.Second-1, queue.Backoff(1, largest), "attempt 1 < 2s")
	assert.Equal(t, 4*time.Second-1, queue.Backoff(2, largest))
	assert.Equal(t, 5*time.Minute-1, queue.Backoff(10, largest), "attempt 10 is capped below 5 min")
	assert.Equal(t, 5*time.Minute-1, queue.Backoff(1000, largest), "no overflow")
	assert.Zero(t, queue.Backoff(3, zero))
	for attempt := range 15 {
		for range 100 {
			d := queue.Backoff(attempt, nil)
			assert.GreaterOrEqual(t, d, time.Duration(0))
			assert.Less(t, d, 5*time.Minute)
		}
	}
}

func TestNewRunner_InvalidConfig(t *testing.T) {
	h := func(context.Context, queue.Job) queue.Result { return queue.Done() }
	for name, change := range map[string]func(*queue.Config){
		"no name":        func(c *queue.Config) { c.Name = "" },
		"zero workers":   func(c *queue.Config) { c.Workers = 0 },
		"zero attempts":  func(c *queue.Config) { c.MaxAttempts = 0 },
		"zero poll":      func(c *queue.Config) { c.PollInterval = 0 },
		"negative lease": func(c *queue.Config) { c.Lease = -time.Second },
		"zero timeout":   func(c *queue.Config) { c.HandlerTimeout = 0 },
		"negative batch": func(c *queue.Config) { c.BatchSize = -1 },
	} {
		cfg := config(1)
		change(&cfg)
		_, err := queue.NewRunner(cfg, queuetest.New(clock.System{}), h, clock.System{}, nil, nil)
		assert.Error(t, err, name)
	}
}

func TestRunner_ClaimErrorKeepsRunning(t *testing.T) {
	store := queuetest.New(clock.System{})
	var fail atomic.Bool
	fail.Store(true)
	store.ClaimFn = func() error {
		if fail.Load() {
			return errors.New("db down")
		}
		return nil
	}
	id := store.Add("job", nil)
	r, err := queue.NewRunner(config(1), store, func(context.Context, queue.Job) queue.Result { return queue.Done() }, clock.System{}, nil, nil)
	require.NoError(t, err)
	start(t, r)
	require.Eventually(t, func() bool { return store.Claims() >= 2 }, 5*time.Second, time.Millisecond)
	fail.Store(false)
	require.Eventually(t, func() bool { return store.Get(id).Status == queuetest.StatusDone }, 5*time.Second, time.Millisecond)
}

// TestRunner_StaleMarkIgnored: runner A claims a job and stalls past its lease; runner B
// re-claims and finishes it; A's late Retry must not undo B's DONE (review T011 round 2).
func TestRunner_StaleMarkIgnored(t *testing.T) {
	store := queuetest.New(clock.System{})
	id := store.Add("job", nil)
	cfg := config(1)
	cfg.Lease = 50 * time.Millisecond

	release := make(chan struct{})
	aStarted := make(chan struct{})
	aCfg := cfg
	aCfg.PollInterval = time.Hour // A claims once, so only B re-claims the expired job
	a, err := queue.NewRunner(aCfg, store, func(context.Context, queue.Job) queue.Result {
		close(aStarted)
		<-release
		return queue.Retry(errors.New("A was too slow"))
	}, clock.System{}, nil, nil)
	require.NoError(t, err)
	stopA := start(t, a)
	<-aStarted

	b, err := queue.NewRunner(cfg, store, func(context.Context, queue.Job) queue.Result { return queue.Done() }, clock.System{}, nil, nil)
	require.NoError(t, err)
	start(t, b)
	require.Eventually(t, func() bool { return store.Get(id).Status == queuetest.StatusDone }, 5*time.Second, time.Millisecond,
		"B re-claims after A's lease expired")

	close(release)
	stopA()
	rec := store.Get(id)
	assert.Equal(t, queuetest.StatusDone, rec.Status, "A's stale Retry did not undo B's DONE")
	assert.Equal(t, 2, rec.Job.Attempts)
	assert.Equal(t, 1, store.StaleMarks())
}

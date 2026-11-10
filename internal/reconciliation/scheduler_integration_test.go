//go:build integration

package reconciliation_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/reconciliation"
)

type runRow struct {
	status         string
	checked, fixed int
	errText        *string
	started, ended time.Time
}

type observer struct {
	mu   sync.Mutex
	runs map[string]int // "job/status"
}

func (o *observer) ObserveReconRun(job, status string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runs[job+"/"+status]++
}
func (o *observer) ObserveReconItems(string, int, int) {}
func (o *observer) count(key string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.runs[key]
}

func setup(t *testing.T) *database.DB {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))
	return db
}

func rows(t *testing.T, db *database.DB, job string) []runRow {
	t.Helper()
	rs, err := db.Pool().Query(context.Background(), `SELECT status, items_checked, items_fixed, error, started_at, finished_at
		FROM job_runs WHERE job_name = $1 ORDER BY started_at`, job)
	require.NoError(t, err)
	defer rs.Close()
	var out []runRow
	for rs.Next() {
		var r runRow
		require.NoError(t, rs.Scan(&r.status, &r.checked, &r.fixed, &r.errText, &r.started, &r.ended))
		out = append(out, r)
	}
	require.NoError(t, rs.Err())
	return out
}

// start runs s until the test ends and checks that Run returns.
func start(t *testing.T, s *reconciliation.Scheduler) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("scheduler did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

// AC-1.
func TestScheduler_RunsAndRecords(t *testing.T) {
	db := setup(t)
	var running, overlaps, runs atomic.Int32
	s := reconciliation.NewScheduler(db, clock.System{}, nil, nil)
	s.Register(reconciliation.Job{Name: "test_job", Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond,
		Run: func(context.Context) reconciliation.Result {
			if running.Add(1) > 1 {
				overlaps.Add(1)
			}
			defer running.Add(-1)
			time.Sleep(10 * time.Millisecond)
			runs.Add(1)
			return reconciliation.Result{Checked: 3, Fixed: 1}
		}})
	stop := start(t, s)
	require.Eventually(t, func() bool { return runs.Load() >= 4 }, 10*time.Second, 10*time.Millisecond)
	stop()

	got := rows(t, db, "test_job")
	require.Len(t, got, int(runs.Load()), "one row per run")
	for _, r := range got {
		assert.Equal(t, "SUCCESS", r.status)
		assert.Equal(t, 3, r.checked)
		assert.Equal(t, 1, r.fixed)
		assert.Nil(t, r.errText)
		assert.False(t, r.ended.Before(r.started))
	}
	assert.Zero(t, overlaps.Load())
}

// AC-2: two instances never run the same job at once; skipped ticks are not stored.
func TestScheduler_SingleRunnerAcrossInstances(t *testing.T) {
	db := setup(t)
	obs := &observer{runs: map[string]int{}}
	var running, overlaps, runs atomic.Int32
	job := reconciliation.Job{Name: "shared_job", Interval: 20 * time.Millisecond, Timeout: 20 * time.Millisecond,
		Run: func(context.Context) reconciliation.Result {
			if running.Add(1) > 1 {
				overlaps.Add(1)
			}
			defer running.Add(-1)
			time.Sleep(15 * time.Millisecond)
			runs.Add(1)
			return reconciliation.Result{}
		}}
	for range 2 {
		s := reconciliation.NewScheduler(db, clock.System{}, nil, obs)
		s.Register(job)
		start(t, s)
	}
	require.Eventually(t, func() bool { return obs.count("shared_job/skipped") >= 3 && runs.Load() >= 5 },
		15*time.Second, 10*time.Millisecond)
	assert.Zero(t, overlaps.Load(), "never two runs at once")
	assert.Equal(t, obs.count("shared_job/success"), len(rows(t, db, "shared_job")), "only real runs are stored")
}

// AC-3: failed, timed-out and panicking runs are FAILED rows; the scheduler and the lock keep working.
func TestScheduler_FailuresRecorded(t *testing.T) {
	db := setup(t)
	var calls atomic.Int32
	s := reconciliation.NewScheduler(db, clock.System{}, nil, nil)
	s.Register(reconciliation.Job{Name: "flaky_job", Interval: 30 * time.Millisecond, Timeout: 30 * time.Millisecond,
		Run: func(ctx context.Context) reconciliation.Result {
			switch calls.Add(1) {
			case 1:
				return reconciliation.Result{Checked: 2, Err: errors.New("bank is down")}
			case 2:
				<-ctx.Done() // runs into the timeout
				return reconciliation.Result{Err: ctx.Err()}
			case 3:
				panic("boom")
			default:
				return reconciliation.Result{Checked: 1}
			}
		}})
	start(t, s)
	require.Eventually(t, func() bool { return len(rows(t, db, "flaky_job")) >= 4 }, 10*time.Second, 10*time.Millisecond)

	got := rows(t, db, "flaky_job")
	require.NotNil(t, got[0].errText)
	assert.Equal(t, "FAILED", got[0].status)
	assert.Equal(t, "bank is down", *got[0].errText)
	assert.Equal(t, 2, got[0].checked)
	assert.Equal(t, "FAILED", got[1].status)
	assert.Contains(t, *got[1].errText, "deadline exceeded")
	assert.Equal(t, "FAILED", got[2].status)
	assert.Equal(t, "panic: boom", *got[2].errText)
	assert.Equal(t, "SUCCESS", got[3].status, "the lock was released after each failure")
}

// AC-3: a long error is truncated.
func TestScheduler_LongErrorTruncated(t *testing.T) {
	db := setup(t)
	s := reconciliation.NewScheduler(db, clock.System{}, nil, nil)
	s.Register(reconciliation.Job{Name: "noisy_job", Interval: time.Hour, Timeout: time.Second,
		Run: func(context.Context) reconciliation.Result {
			return reconciliation.Result{Err: errors.New(strings.Repeat("x", 5000))}
		}})
	ran, res, err := s.RunOnce(context.Background(), "noisy_job")
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Error(t, res.Err)
	got := rows(t, db, "noisy_job")
	require.Len(t, got, 1)
	assert.Len(t, *got[0].errText, 2000)
}

// AC-5: cancel stops Run after the in-flight run, which is still recorded.
func TestScheduler_Shutdown(t *testing.T) {
	db := setup(t)
	entered := make(chan struct{})
	var once sync.Once
	s := reconciliation.NewScheduler(db, clock.System{}, nil, nil)
	s.Register(reconciliation.Job{Name: "slow_job", Interval: 20 * time.Millisecond, Timeout: 20 * time.Millisecond,
		Run: func(ctx context.Context) reconciliation.Result {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return reconciliation.Result{Checked: 7}
		}})
	stop := start(t, s)
	<-entered
	stop()
	got := rows(t, db, "slow_job")
	require.NotEmpty(t, got)
	assert.Equal(t, 7, got[len(got)-1].checked)
	require.ErrorContains(t, s.Run(context.Background()), "twice")
}

// AC-6 at the package level: RunOnce skips while another session holds the lock.
func TestScheduler_RunOnceSkipsWhileLocked(t *testing.T) {
	db := setup(t)
	s := reconciliation.NewScheduler(db, clock.System{}, nil, nil)
	var calls atomic.Int32
	s.Register(reconciliation.Job{Name: "locked_job", Interval: time.Hour, Timeout: time.Second,
		Run: func(context.Context) reconciliation.Result { calls.Add(1); return reconciliation.Result{} }})

	conn, err := db.Pool().Acquire(context.Background())
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, reconciliation.LockKey("locked_job"))
	require.NoError(t, err)

	ran, _, err := s.RunOnce(context.Background(), "locked_job")
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Zero(t, calls.Load())
	assert.Empty(t, rows(t, db, "locked_job"))

	_, err = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, reconciliation.LockKey("locked_job"))
	require.NoError(t, err)
	ran, _, err = s.RunOnce(context.Background(), "locked_job")
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Len(t, rows(t, db, "locked_job"), 1)
}

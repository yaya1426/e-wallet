// Package reconciliation runs the background jobs that find and resolve what the normal
// flows could not (TDR-009): lost webhooks, dead outbox events, stuck transactions. This file
// is the scheduler: each registered job runs on its interval, one instance at a time across
// all wallet processes (a Postgres advisory lock), and every real run is recorded in
// job_runs. The jobs themselves are registered by cmd/wallet.
package reconciliation

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/reconciliation/internal/sqlcgen"
)

// Run statuses stored in job_runs.
const (
	StatusSuccess = "SUCCESS"
	StatusFailed  = "FAILED"
)

// Metric label for a tick skipped because another instance held the lock (not stored).
const statusSkipped = "skipped"

const (
	// recordTimeout bounds unlocking and recording a run; it applies even during shutdown.
	recordTimeout = 5 * time.Second
	// maxErrorLength bounds job_runs.error.
	maxErrorLength = 2000
)

// Result is what one run of a job did. A non-nil Err makes the run FAILED.
type Result struct {
	Checked int // items looked at
	Fixed   int // items resolved
	Err     error
}

// Job is a reconciliation job.
type Job struct {
	Name     string        // [a-z_]+, unique; also the advisory lock and the job_runs key
	Interval time.Duration // time between the end of one run and the next tick, ±10 % jitter
	Timeout  time.Duration // bounds one run; 0 < Timeout ≤ Interval
	Run      func(ctx context.Context) Result
}

// Observer receives the scheduler's metrics. status is "success", "failed" or "skipped".
type Observer interface {
	ObserveReconRun(job, status string, duration time.Duration)
	ObserveReconItems(job string, checked, fixed int)
}

type noopObserver struct{}

func (noopObserver) ObserveReconRun(string, string, time.Duration) {}
func (noopObserver) ObserveReconItems(string, int, int)            {}

// ErrUnknownJob means no job is registered with the name.
var ErrUnknownJob = errors.New("reconciliation: unknown job")

var validName = regexp.MustCompile(`^[a-z_]+$`)

// Scheduler runs the registered jobs.
type Scheduler struct {
	pool  *pgxpool.Pool
	clock clock.Clock
	log   *slog.Logger
	obs   Observer

	mu      sync.RWMutex
	jobs    []Job
	started atomic.Bool
	// jitter returns the wait before the next tick; tests may replace it.
	jitter func(time.Duration) time.Duration
}

// NewScheduler returns a scheduler without jobs. log and obs may be nil.
func NewScheduler(db *database.DB, clk clock.Clock, log *slog.Logger, obs Observer) *Scheduler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if obs == nil {
		obs = noopObserver{}
	}
	return &Scheduler{pool: db.Pool(), clock: clk, log: log, obs: obs, jitter: jittered}
}

// Register adds a job. Call it while wiring, before Run; an invalid or duplicate job, or a
// call after Run started, is a wiring bug and panics.
func (s *Scheduler) Register(j Job) {
	if s.started.Load() {
		panic(fmt.Sprintf("reconciliation: Register(%q) after Run", j.Name))
	}
	switch {
	case !validName.MatchString(j.Name):
		panic(fmt.Sprintf("reconciliation: invalid job name %q", j.Name))
	case j.Interval <= 0:
		panic(fmt.Sprintf("reconciliation: job %q: interval must be positive", j.Name))
	case j.Timeout <= 0 || j.Timeout > j.Interval:
		panic(fmt.Sprintf("reconciliation: job %q: timeout must be in (0, interval]", j.Name))
	case j.Run == nil:
		panic(fmt.Sprintf("reconciliation: job %q has no Run function", j.Name))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.jobs, func(o Job) bool { return o.Name == j.Name }) {
		panic(fmt.Sprintf("reconciliation: job %q registered twice", j.Name))
	}
	s.jobs = append(s.jobs, j)
}

// Jobs returns the registered job names in registration order.
func (s *Scheduler) Jobs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, len(s.jobs))
	for i, j := range s.jobs {
		names[i] = j.Name
	}
	return names
}

func (s *Scheduler) job(name string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := slices.IndexFunc(s.jobs, func(j Job) bool { return j.Name == name })
	if i < 0 {
		return Job{}, false
	}
	return s.jobs[i], true
}

// Run runs every job on its interval until ctx ends, then waits for the running ones to
// finish (each bounded by its Timeout) and returns nil. The first tick of a job comes one
// jittered interval after the start, so a crash loop does not hammer the bank.
func (s *Scheduler) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("reconciliation: Run called twice")
	}
	s.mu.RLock()
	jobs := slices.Clone(s.jobs)
	s.mu.RUnlock()
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() { s.loop(ctx, j) })
	}
	wg.Wait()
	return nil
}

// loop ticks one job. The next wait starts after the run ended, so runs never overlap.
func (s *Scheduler) loop(ctx context.Context, j Job) {
	for {
		timer := time.NewTimer(s.jitter(j.Interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if _, _, err := s.tick(ctx, j); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "reconciliation tick failed", slog.String("job", j.Name), slog.Any("error", err))
		}
	}
}

// RunOnce runs the named job once now, if no other instance is running it. ran is false
// when it was skipped because of the lock.
func (s *Scheduler) RunOnce(ctx context.Context, name string) (ran bool, res Result, err error) {
	j, ok := s.job(name)
	if !ok {
		return false, Result{}, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownJob, name, s.Jobs())
	}
	return s.tick(ctx, j)
}

// tick takes the job's advisory lock on one connection, runs the job, releases the lock on
// that same connection and records the run. err is about the machinery (lock, record), not
// the job's result.
func (s *Scheduler) tick(ctx context.Context, j Job) (bool, Result, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, Result{}, fmt.Errorf("reconciliation: acquire connection: %w", err)
	}
	key := LockKey(j.Name)
	acquired, err := sqlcgen.New(conn).TryAdvisoryLock(ctx, key)
	if err != nil {
		conn.Release()
		return false, Result{}, fmt.Errorf("reconciliation: lock %s: %w", j.Name, err)
	}
	if !acquired {
		conn.Release()
		s.obs.ObserveReconRun(j.Name, statusSkipped, 0)
		s.log.DebugContext(ctx, "reconciliation job running elsewhere; skipped", slog.String("job", j.Name))
		return false, Result{}, nil
	}

	started := s.clock.Now()
	res := s.call(ctx, j)
	finished := s.clock.Now()

	// Unlock and record even when ctx was cancelled (shutdown), within recordTimeout.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := sqlcgen.New(conn).UnlockAdvisory(rctx, key); err != nil {
		// Never return a connection that may still hold the lock to the pool: closing the
		// session releases it.
		_ = conn.Hijack().Close(rctx)
		s.log.ErrorContext(ctx, "reconciliation unlock failed; connection closed", slog.String("job", j.Name), slog.Any("error", err))
	} else {
		conn.Release()
	}
	recordErr := s.record(rctx, j.Name, started, finished, res)

	status := StatusSuccess
	if res.Err != nil {
		status = StatusFailed
	}
	duration := finished.Sub(started)
	s.obs.ObserveReconRun(j.Name, lower(status), duration)
	s.obs.ObserveReconItems(j.Name, res.Checked, res.Fixed)
	attrs := []any{slog.String("job", j.Name), slog.String("status", status), slog.Int("items_checked", res.Checked),
		slog.Int("items_fixed", res.Fixed), slog.Duration("duration", duration)}
	if res.Err != nil {
		s.log.WarnContext(ctx, "reconciliation run failed", append(attrs, slog.Any("error", res.Err))...)
	} else {
		s.log.InfoContext(ctx, "reconciliation run finished", attrs...)
	}
	return true, res, recordErr
}

// call runs the job with its timeout; a panic becomes a failed result.
func (s *Scheduler) call(ctx context.Context, j Job) (res Result) {
	jctx, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			s.log.ErrorContext(ctx, "reconciliation job panicked", slog.String("job", j.Name),
				slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
			res = Result{Err: fmt.Errorf("panic: %v", v)}
		}
	}()
	return j.Run(jctx)
}

func (s *Scheduler) record(ctx context.Context, name string, started, finished time.Time, res Result) error {
	status := StatusSuccess
	var errText *string
	if res.Err != nil {
		status = StatusFailed
		msg := res.Err.Error()
		if len(msg) > maxErrorLength {
			msg = msg[:maxErrorLength]
		}
		errText = &msg
	}
	err := sqlcgen.New(s.pool).InsertJobRun(ctx, sqlcgen.InsertJobRunParams{
		ID: id.New(), JobName: name, StartedAt: started, FinishedAt: finished, Status: status,
		ItemsChecked: toInt32(res.Checked), ItemsFixed: toInt32(res.Fixed), Error: errText,
	})
	if err != nil {
		return fmt.Errorf("reconciliation: record run of %s: %w", name, err)
	}
	return nil
}

// LockKey is the advisory lock key of a job: the first 8 bytes of SHA-256("recon:"+name).
// It is stable across versions, so old and new instances agree during a rolling deploy.
func LockKey(name string) int64 {
	sum := sha256.Sum256([]byte("recon:" + name))
	return int64(binary.BigEndian.Uint64(sum[:8])) //nolint:gosec // a hash reinterpreted as a signed key, overflow is intended
}

// jittered returns d ± 10 %, uniformly.
func jittered(d time.Duration) time.Duration {
	spread := int64(d) / 5
	if spread <= 0 {
		return d
	}
	return d - time.Duration(spread/2) + time.Duration(rand.Int64N(spread+1)) //nolint:gosec // jitter, not security
}

func lower(status string) string {
	if status == StatusSuccess {
		return "success"
	}
	return "failed"
}

func toInt32(n int) int32 {
	return int32(max(0, min(n, 1<<31-1)))
}

// JobInfo describes a registered job.
type JobInfo struct {
	Name     string
	Interval time.Duration
}

// Registered returns the registered jobs and their intervals.
func (s *Scheduler) Registered() []JobInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]JobInfo, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = JobInfo{Name: j.Name, Interval: j.Interval}
	}
	return out
}

// LastRun is the latest recorded run of a job.
type LastRun struct {
	Status     string
	FinishedAt time.Time
}

// LastRuns returns the latest run of every job that ran at least once (ops health, T028).
func (s *Scheduler) LastRuns(ctx context.Context) (map[string]LastRun, error) {
	rows, err := sqlcgen.New(s.pool).LastRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: last runs: %w", err)
	}
	out := make(map[string]LastRun, len(rows))
	for _, r := range rows {
		out[r.JobName] = LastRun{Status: r.Status, FinishedAt: r.FinishedAt}
	}
	return out, nil
}

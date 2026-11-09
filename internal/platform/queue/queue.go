// Package queue is the database-backed job queue engine behind the outbox (TDR-006) and the
// webhook inbox (TDR-007): one dispatcher goroutine claims due jobs from a Store and a fixed
// pool of workers runs them. It knows nothing about tables; each owner module implements
// Store over its own table.
package queue

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
)

// Job is one claimed unit of work.
type Job struct {
	ID       uuid.UUID
	Type     string
	Payload  []byte
	Attempts int // including the current attempt
}

type resultKind int

const (
	resultDone resultKind = iota
	resultRetry
	resultFail
)

// Result is what a handler decided about a job.
type Result struct {
	kind   resultKind
	err    error
	reason string
}

// Done marks the job finished.
func Done() Result { return Result{kind: resultDone} }

// Retry schedules the job again after a backoff, or marks it DEAD once MaxAttempts is reached.
// Use it for temporary failures (TDR-006 §3).
func Retry(err error) Result { return Result{kind: resultRetry, err: err} }

// Fail finishes the job as failed for a permanent reason (no retry).
func Fail(reason string) Result { return Result{kind: resultFail, reason: reason} }

// Handler processes one job. It must be idempotent: jobs are delivered at least once.
type Handler func(ctx context.Context, job Job) Result

// Store is the owner module's table of jobs.
type Store interface {
	// Claim claims up to n due jobs (PENDING with next_attempt_at <= now, or PROCESSING with an
	// expired lease) with FOR UPDATE SKIP LOCKED: it sets PROCESSING, locked_by = owner,
	// locked_until = now + lease and attempts + 1, and returns them.
	Claim(ctx context.Context, owner string, n int, lease time.Duration) ([]Job, error)
	// The Mark* calls finish the claim job came from. A store must update the row only if it
	// is still PROCESSING with job.Attempts: when this runner's lease expired and another
	// runner re-claimed the job (attempts + 1), the stale mark changes nothing.
	MarkDone(ctx context.Context, job Job) error
	MarkRetry(ctx context.Context, job Job, next time.Time, lastErr string) error
	MarkDead(ctx context.Context, job Job, lastErr string) error
	MarkFailed(ctx context.Context, job Job, reason string) error
}

// Observer receives queue events, e.g. to count them in metrics. Results are "done",
// "retry", "dead" and "failed".
type Observer interface {
	ObserveQueueClaimed(queue string, n int)
	ObserveQueueJob(queue, result string, handlerDuration time.Duration)
}

// Config configures a Runner.
type Config struct {
	Name           string        // queue name in logs and metrics, e.g. "outbox"
	Workers        int           // concurrent handlers, ≥ 1
	PollInterval   time.Duration // how often the dispatcher looks for due jobs
	Lease          time.Duration // how long a claimed job stays reserved for this runner
	HandlerTimeout time.Duration // per job
	MaxAttempts    int           // attempts before a retried job becomes DEAD, ≥ 1
	BatchSize      int           // channel capacity; 0 means 2 × Workers
}

func (c Config) validate() error {
	var errs []error
	if c.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if c.Workers < 1 {
		errs = append(errs, fmt.Errorf("workers must be ≥ 1, got %d", c.Workers))
	}
	if c.MaxAttempts < 1 {
		errs = append(errs, fmt.Errorf("max attempts must be ≥ 1, got %d", c.MaxAttempts))
	}
	if c.BatchSize < 0 {
		errs = append(errs, fmt.Errorf("batch size must not be negative, got %d", c.BatchSize))
	}
	for name, d := range map[string]time.Duration{"poll interval": c.PollInterval, "lease": c.Lease, "handler timeout": c.HandlerTimeout} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", name, d))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("queue: invalid config: %w", err)
	}
	return nil
}

// markTimeout bounds each Mark* call; it runs even while the runner shuts down.
const markTimeout = 5 * time.Second

// Runner runs a queue: one dispatcher and Workers worker goroutines.
type Runner struct {
	cfg     Config
	store   Store
	handler Handler
	clock   clock.Clock
	log     *slog.Logger
	obs     Observer
	owner   string
	wake    chan struct{}
	rnd     func(n int64) int64 // backoff jitter; nil means math/rand/v2
}

// NewRunner validates cfg and returns a Runner. log and obs may be nil.
func NewRunner(cfg Config, store Store, h Handler, clk clock.Clock, log *slog.Logger, obs Observer) (*Runner, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 2 * cfg.Workers
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if obs == nil {
		obs = noopObserver{}
	}
	return &Runner{
		cfg: cfg, store: store, handler: h, clock: clk, log: log.With(slog.String("queue", cfg.Name)), obs: obs,
		owner: ownerID(), wake: make(chan struct{}, 1),
	}, nil
}

// Wake asks the dispatcher to claim now instead of at the next poll tick. It never blocks;
// extra wakes while one is pending are dropped.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run claims and runs jobs until ctx ends. Then it stops claiming, does not start jobs still
// waiting in the channel (their lease expires and another claim picks them up), lets running
// handlers finish within HandlerTimeout, and returns nil once every worker has exited.
func (r *Runner) Run(ctx context.Context) error {
	jobs := make(chan Job, r.cfg.BatchSize)
	var wg sync.WaitGroup
	for range r.cfg.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.work(ctx, jobs)
		}()
	}
	r.dispatch(ctx, jobs)
	close(jobs)
	wg.Wait()
	return nil
}

// dispatch claims due jobs on every poll tick or wake-up while there is room in the channel.
func (r *Runner) dispatch(ctx context.Context, jobs chan<- Job) {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		r.claim(ctx, jobs)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Runner) claim(ctx context.Context, jobs chan<- Job) {
	free := cap(jobs) - len(jobs)
	if free <= 0 || ctx.Err() != nil {
		return
	}
	claimed, err := r.store.Claim(ctx, r.owner, free, r.cfg.Lease)
	if err != nil {
		if ctx.Err() == nil {
			r.log.WarnContext(ctx, "queue claim failed", slog.Any("error", err))
		}
		return
	}
	if len(claimed) > 0 {
		r.obs.ObserveQueueClaimed(r.cfg.Name, len(claimed))
	}
	for _, job := range claimed {
		jobs <- job // only the dispatcher sends, and free slots were counted above
	}
}

// work runs jobs from the channel until it is closed or ctx ends.
func (r *Runner) work(ctx context.Context, jobs <-chan Job) {
	for job := range jobs {
		if ctx.Err() != nil {
			return // do not start new jobs while shutting down
		}
		r.runOne(ctx, job)
	}
}

func (r *Runner) runOne(ctx context.Context, job Job) {
	// A running handler is allowed to finish during shutdown, bounded by its own timeout.
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.HandlerTimeout)
	start := r.clock.Now()
	res := r.call(hctx, job)
	cancel()
	duration := r.clock.Now().Sub(start)

	mctx, mcancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer mcancel()
	log := r.log.With(slog.String("job_id", job.ID.String()), slog.String("type", job.Type), slog.Int("attempts", job.Attempts))
	var (
		outcome string
		err     error
	)
	switch {
	case res.kind == resultDone:
		outcome, err = "done", r.store.MarkDone(mctx, job)
	case res.kind == resultFail:
		outcome, err = "failed", r.store.MarkFailed(mctx, job, res.reason)
		log.WarnContext(ctx, "queue job failed", slog.String("reason", res.reason))
	case job.Attempts >= r.cfg.MaxAttempts:
		outcome, err = "dead", r.store.MarkDead(mctx, job, errText(res.err))
		log.ErrorContext(ctx, "queue job dead", slog.Any("error", res.err))
	default:
		next := r.clock.Now().Add(Backoff(job.Attempts, r.rnd))
		outcome, err = "retry", r.store.MarkRetry(mctx, job, next, errText(res.err))
		log.WarnContext(ctx, "queue job will be retried", slog.Any("error", res.err), slog.Time("next_attempt_at", next))
	}
	r.obs.ObserveQueueJob(r.cfg.Name, outcome, duration)
	if err != nil {
		// The lease will expire and the job will be claimed again (at-least-once).
		log.ErrorContext(ctx, "queue mark failed", slog.String("result", outcome), slog.Any("error", err))
	}
}

// call runs the handler; a panic becomes a Retry so the runner keeps working.
func (r *Runner) call(ctx context.Context, job Job) (res Result) {
	defer func() {
		if v := recover(); v != nil {
			r.log.ErrorContext(ctx, "queue handler panicked", slog.String("job_id", job.ID.String()),
				slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
			res = Retry(fmt.Errorf("handler panic: %v", v))
		}
	}()
	return r.handler(ctx, job)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ownerID identifies this runner in locked_by: host name, process ID and a random suffix.
func ownerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), rand.Text()[:8])
}

type noopObserver struct{}

func (noopObserver) ObserveQueueClaimed(string, int)               {}
func (noopObserver) ObserveQueueJob(string, string, time.Duration) {}

package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yaya1426/e-wallet/internal/outbox/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

const (
	// QueueName names the outbox in logs and the queue_* metrics.
	QueueName = "outbox"
	// DefaultLease is how long a claimed event stays reserved for one runner (TDR-006 §2).
	DefaultLease = 60 * time.Second
	// DefaultHandlerTimeout bounds one delivery, e.g. the bank call (TDR-006 §2).
	DefaultHandlerTimeout = 10 * time.Second
	// backlogInterval is how often the backlog gauges are refreshed (TDR-006 §6).
	backlogInterval = 15 * time.Second
)

// Config configures delivery. Lease and HandlerTimeout default to DefaultLease and
// DefaultHandlerTimeout when zero.
type Config struct {
	Workers        int           // OUTBOX_WORKERS
	MaxAttempts    int           // OUTBOX_MAX_ATTEMPTS
	PollInterval   time.Duration // QUEUE_POLL_INTERVAL
	Lease          time.Duration
	HandlerTimeout time.Duration
}

// Service enqueues events and delivers them.
type Service struct {
	db     Querier // the pool, used by the runner
	clock  clock.Clock
	log    *slog.Logger
	obs    Observer
	runner *queue.Runner

	mu       sync.RWMutex
	handlers map[string]Handler
	started  atomic.Bool
}

// NewService returns the outbox. db is the pool the runner claims and marks events with. log
// and obs may be nil.
func NewService(db Querier, clk clock.Clock, cfg Config, log *slog.Logger, obs Observer) (*Service, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if obs == nil {
		obs = noopObserver{}
	}
	if cfg.Lease == 0 {
		cfg.Lease = DefaultLease
	}
	if cfg.HandlerTimeout == 0 {
		cfg.HandlerTimeout = DefaultHandlerTimeout
	}
	s := &Service{db: db, clock: clk, log: log, obs: obs, handlers: make(map[string]Handler)}
	runner, err := queue.NewRunner(queue.Config{
		Name: QueueName, Workers: cfg.Workers, PollInterval: cfg.PollInterval, Lease: cfg.Lease,
		HandlerTimeout: cfg.HandlerTimeout, MaxAttempts: cfg.MaxAttempts,
	}, store{db: db, clock: clk, obs: obs}, s.deliver, clk, log, obs)
	if err != nil {
		return nil, fmt.Errorf("outbox: %w", err)
	}
	s.runner = runner
	return s, nil
}

// Register sets the handler of an event type. Call it while wiring, before Run; registering a
// type twice, or after Run started, is a wiring bug and panics.
func (s *Service) Register(eventType string, h Handler) {
	if s.started.Load() {
		panic(fmt.Sprintf("outbox: Register(%q) after Run", eventType))
	}
	if h == nil {
		panic(fmt.Sprintf("outbox: Register(%q) with a nil handler", eventType))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.handlers[eventType]; dup {
		panic(fmt.Sprintf("outbox: event type %q registered twice", eventType))
	}
	s.handlers[eventType] = h
}

func (s *Service) handler(eventType string) (Handler, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handlers[eventType]
	return h, ok
}

// Enqueue inserts ev as PENDING in tx, the caller's unit of work: it is delivered only if tx
// commits. After the commit, call Wake. An event type without a registered handler fails
// with ErrUnknownEventType, so wiring mistakes show up at once.
func (s *Service) Enqueue(ctx context.Context, tx Querier, ev Event) error {
	if _, ok := s.handler(ev.Type); !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEventType, ev.Type)
	}
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("outbox: encode %q payload: %w", ev.Type, err)
	}
	err = queries(tx).InsertEvent(ctx, sqlcgen.InsertEventParams{
		ID: id.New(), AggregateType: ev.AggregateType, AggregateID: ev.AggregateID,
		EventType: ev.Type, Payload: payload, Now: s.clock.Now(),
	})
	return wrap("enqueue", err)
}

// Wake asks the runner to claim now instead of at the next poll. It never blocks. Call it
// after the enqueuing transaction committed, never inside it (TDR-006 §2). Without a
// running runner (OUTBOX_ENABLED=false) it does nothing useful and is harmless.
func (s *Service) Wake() { s.runner.Wake() }

// Run delivers events and refreshes the backlog gauges until ctx ends, then waits for the
// running deliveries to finish (see queue.Runner.Run). It returns nil on shutdown.
func (s *Service) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("outbox: Run called twice")
	}
	var wg sync.WaitGroup
	wg.Go(func() { s.reportBacklog(ctx) })
	err := s.runner.Run(ctx)
	wg.Wait()
	return err
}

// deliver is the queue handler: it calls the handler registered for the event type. An
// unknown type (e.g. enqueued by a newer version during a rolling deploy) is retried, so it
// is delivered once an instance knows it, or becomes DEAD.
func (s *Service) deliver(ctx context.Context, job queue.Job) queue.Result {
	h, ok := s.handler(job.Type)
	if !ok {
		return queue.Retry(fmt.Errorf("%w: %q", ErrUnknownEventType, job.Type))
	}
	return h(ctx, job)
}

func (s *Service) reportBacklog(ctx context.Context) {
	ticker := time.NewTicker(backlogInterval)
	defer ticker.Stop()
	for {
		if err := s.RefreshBacklog(ctx); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "outbox backlog refresh failed", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RefreshBacklog reads the backlog with one query and reports it to the observer.
func (s *Service) RefreshBacklog(ctx context.Context) error {
	now := s.clock.Now()
	row, err := queries(s.db).Backlog(ctx, now)
	if err != nil {
		return fmt.Errorf("outbox: backlog: %w", err)
	}
	var age time.Duration
	if row.Pending > 0 { // PostgreSQL keeps microseconds, so an empty backlog is not exactly now
		age = max(0, now.Sub(row.Oldest))
	}
	s.obs.ObserveOutboxBacklog(row.Pending, age)
	return nil
}

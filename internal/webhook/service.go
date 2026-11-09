package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/webhook/internal/sqlcgen"
)

const (
	// QueueName names the inbox in logs and the queue_* metrics.
	QueueName = "inbox"
	// DefaultLease is how long a claimed event stays reserved for one worker.
	DefaultLease = 60 * time.Second
	// DefaultHandlerTimeout bounds processing one event (one DB transaction).
	DefaultHandlerTimeout = 10 * time.Second
)

// Config configures the inbox workers. Lease and HandlerTimeout default to DefaultLease and
// DefaultHandlerTimeout when zero.
type Config struct {
	Workers        int           // INBOX_WORKERS
	MaxAttempts    int           // INBOX_MAX_ATTEMPTS
	PollInterval   time.Duration // QUEUE_POLL_INTERVAL
	Lease          time.Duration
	HandlerTimeout time.Duration
}

// Service stores bank events and processes them.
type Service struct {
	db            Querier
	tx            Transactor
	discrepancies Recorder
	maxAttempts   int
	clock         clock.Clock
	log           *slog.Logger
	obs           Observer
	runner        *queue.Runner

	mu      sync.RWMutex
	handler EventHandler
	started atomic.Bool
}

// NewService returns the inbox. db is the pool used to store, claim and mark events; tx runs
// each event's handler in its own transaction; discrepancies records UNKNOWN_BANK_EVENT. log
// and obs may be nil.
func NewService(db Querier, tx Transactor, discrepancies Recorder, clk clock.Clock, cfg Config, log *slog.Logger, obs Observer) (*Service, error) {
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
	if discrepancies == nil {
		return nil, errors.New("webhook: a discrepancy recorder is required")
	}
	s := &Service{db: db, tx: tx, discrepancies: discrepancies, maxAttempts: cfg.MaxAttempts, clock: clk, log: log, obs: obs}
	runner, err := queue.NewRunner(queue.Config{
		Name: QueueName, Workers: cfg.Workers, PollInterval: cfg.PollInterval, Lease: cfg.Lease,
		HandlerTimeout: cfg.HandlerTimeout, MaxAttempts: cfg.MaxAttempts,
	}, store{db: db, clock: clk}, s.process, clk, log, obs)
	if err != nil {
		return nil, fmt.Errorf("webhook: %w", err)
	}
	s.runner = runner
	return s, nil
}

// SetHandler sets the handler the workers call. Call it while wiring, before Run; a nil
// handler or a call after Run is a wiring bug and panics.
func (s *Service) SetHandler(h EventHandler) {
	if s.started.Load() {
		panic("webhook: SetHandler after Run")
	}
	if h == nil {
		panic("webhook: SetHandler with a nil handler")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = h
}

// HasHandler reports whether a handler is set, i.e. whether Run can process events.
func (s *Service) HasHandler() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler != nil
}

// Store saves a verified event as PENDING unless (Provider, eventID) is already stored. It
// reports whether a row was inserted (false for a duplicate). After a store, the workers
// are woken.
func (s *Service) Store(ctx context.Context, eventID, eventType string, payload []byte) (bool, error) {
	n, err := queries(s.db).InsertEvent(ctx, sqlcgen.InsertEventParams{
		ID: id.New(), Provider: Provider, ProviderEventID: eventID, EventType: eventType,
		Payload: payload, Now: s.clock.Now(),
	})
	if err != nil {
		return false, fmt.Errorf("webhook: store event: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	s.runner.Wake()
	return true, nil
}

// Run processes events until ctx ends, then waits for the running ones to finish (see
// queue.Runner.Run). It needs a handler, and returns nil on shutdown.
func (s *Service) Run(ctx context.Context) error {
	if !s.HasHandler() {
		return errors.New("webhook: Run without a handler")
	}
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("webhook: Run called twice")
	}
	return s.runner.Run(ctx)
}

// process is the queue handler for one inbox event (TDR-007 §3). An event that can never be
// applied fails at once; otherwise the handler runs in its own transaction and any error,
// including ErrUnknownTransaction, is retried until the event becomes DEAD. When the last
// attempt still finds no transaction, an UNKNOWN_BANK_EVENT discrepancy is recorded first.
func (s *Service) process(ctx context.Context, job queue.Job) queue.Result {
	ev, err := decodeEvent(job.Payload)
	if err != nil {
		s.log.WarnContext(ctx, "bank event rejected", slog.String("inbox_id", job.ID.String()),
			slog.String("event_type", job.Type), slog.Any("error", err))
		return queue.Fail(err.Error())
	}
	log := s.log.With(slog.String("event_id", ev.EventID), slog.String("event_type", ev.Type),
		slog.String("bank_reference", ev.BankReference), slog.Int("attempts", job.Attempts))

	s.mu.RLock()
	h := s.handler
	s.mu.RUnlock()
	err = s.tx.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return h.HandleBankEvent(ctx, tx, ev)
	})
	switch {
	case err == nil:
		log.InfoContext(ctx, "bank event processed", slog.String("result", "done"))
		return queue.Done()
	case errors.Is(err, ErrUnknownTransaction):
		if job.Attempts >= s.maxAttempts {
			s.recordUnknown(ctx, log, ev)
			return queue.Retry(err) // the runner marks it DEAD: attempts are used up
		}
		log.WarnContext(ctx, "bank event for an unknown transaction", slog.String("result", "retry"))
		return queue.Retry(err)
	default:
		log.WarnContext(ctx, "bank event processing failed", slog.String("result", "retry"), slog.Any("error", err))
		return queue.Retry(err)
	}
}

// recordUnknown records UNKNOWN_BANK_EVENT for an event no transaction matched after the
// last attempt (TDR-007 §3). It runs in its own transaction: the handler's was rolled back.
// A failure is logged; the DEAD event itself keeps the payload for an operator.
func (s *Service) recordUnknown(ctx context.Context, log *slog.Logger, ev BankEvent) {
	err := s.tx.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.discrepancies.Record(ctx, tx, discrepancy.New{
			Kind: discrepancy.KindUnknownBankEvent, Severity: discrepancy.SeverityWarning,
			Details: map[string]any{
				"provider": Provider, "event_id": ev.EventID, "event_type": ev.Type,
				"bank_reference": ev.BankReference, "idempotency_key": ev.IdempotencyKey,
				"amount_minor": ev.Amount.Amount(), "currency": ev.Amount.Currency(),
			},
		})
	})
	if err != nil {
		log.ErrorContext(ctx, "unknown bank event: discrepancy not recorded", slog.Any("error", err))
		return
	}
	log.ErrorContext(ctx, "bank event for an unknown transaction is dead", slog.String("result", "dead"))
}

// wireEvent is the webhook body (TDR-007 §4). Unknown fields are ignored so the bank can add
// fields without breaking us.
type wireEvent struct {
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	BankReference  string    `json:"bank_reference"`
	IdempotencyKey uuid.UUID `json:"idempotency_key"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// statusOf maps each known event type to the status it must carry.
var statusOf = map[string]string{
	EventDebitSucceeded:  StatusSucceeded,
	EventDebitFailed:     StatusFailed,
	EventPayoutSucceeded: StatusSucceeded,
	EventPayoutFailed:    StatusFailed,
}

// decodeEvent decodes and validates a stored payload. Its errors are permanent: the same
// payload would fail again.
func decodeEvent(payload []byte) (BankEvent, error) {
	var w wireEvent
	if err := json.Unmarshal(payload, &w); err != nil {
		return BankEvent{}, fmt.Errorf("undecodable payload: %w", err)
	}
	wantStatus, known := statusOf[w.EventType]
	if !known {
		return BankEvent{}, fmt.Errorf("unknown event_type %q", w.EventType)
	}
	currency, err := money.ParseCurrency(w.Currency)
	if err != nil {
		return BankEvent{}, fmt.Errorf("invalid currency: %w", err)
	}
	amount, err := money.NewPositive(w.AmountMinor, currency)
	if err != nil {
		return BankEvent{}, fmt.Errorf("invalid amount_minor: %w", err)
	}
	switch {
	case w.EventID == "":
		return BankEvent{}, errors.New("event_id is missing")
	case w.BankReference == "":
		return BankEvent{}, errors.New("bank_reference is missing")
	case w.IdempotencyKey == uuid.Nil:
		return BankEvent{}, errors.New("idempotency_key is missing")
	case w.Status != wantStatus:
		return BankEvent{}, fmt.Errorf("status %q does not match event_type %q", w.Status, w.EventType)
	case w.OccurredAt.IsZero():
		return BankEvent{}, errors.New("occurred_at is missing")
	}
	return BankEvent{
		EventID: w.EventID, Type: w.EventType, BankReference: w.BankReference, IdempotencyKey: w.IdempotencyKey,
		Amount: amount, Status: w.Status, FailureCode: w.FailureCode, FailureMessage: w.FailureMessage,
		OccurredAt: w.OccurredAt,
	}, nil
}

// Package outbox is the transactional outbox (TDR-006): business code enqueues an event in
// its own database transaction, and the outbox runner delivers it to the handler registered
// for its type, at least once. Delivery runs on the generic queue engine
// (internal/platform/queue); this package owns the outbox_events table.
package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

// Event is a message to deliver after the enqueuing transaction commits.
type Event struct {
	AggregateType string    // e.g. "transaction"
	AggregateID   uuid.UUID // e.g. the transaction ID
	Type          string    // e.g. "withdrawal.requested"; must have a registered handler
	Payload       any       // stored as JSON (encoding/json)
}

// Handler delivers one event. It decodes job.Payload itself and returns queue.Done,
// queue.Retry or queue.Fail. It must be idempotent: an event can be delivered more than once.
type Handler = queue.Handler

// ErrUnknownEventType means no handler is registered for the event type.
var ErrUnknownEventType = errors.New("outbox: unknown event type")

// Querier runs SQL: a pgx.Tx inside a unit of work, or the pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Observer receives the outbox metrics (TDR-006 §6) and the queue engine's events.
type Observer interface {
	queue.Observer
	// ObserveOutboxBacklog reports the events not yet finished (PENDING or PROCESSING) and
	// the age of the oldest one (0 when there are none).
	ObserveOutboxBacklog(pending int64, oldestAge time.Duration)
	// ObserveOutboxDead counts one event that became DEAD.
	ObserveOutboxDead()
}

type noopObserver struct{}

func (noopObserver) ObserveQueueClaimed(string, int)               {}
func (noopObserver) ObserveQueueJob(string, string, time.Duration) {}
func (noopObserver) ObserveOutboxBacklog(int64, time.Duration)     {}
func (noopObserver) ObserveOutboxDead()                            {}

// Package webhook ingests the bank's webhooks through an inbox (TDR-007): the HTTP handler
// verifies the signature, stores the event once in inbox_events and answers 200 at once;
// inbox workers then hand each event to the registered EventHandler (payment) in its own
// database transaction. This package owns the inbox_events table and applies no business
// rule itself.
package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

// Provider is the inbox_events.provider of the phase-1 bank (the simulator).
const Provider = "banksim"

// Event types sent by the bank (TDR-007 §4).
const (
	EventDebitSucceeded  = "debit.succeeded"
	EventDebitFailed     = "debit.failed"
	EventPayoutSucceeded = "payout.succeeded"
	EventPayoutFailed    = "payout.failed"
)

// Bank operation statuses in an event.
const (
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
)

// BankEvent is a decoded and validated bank event (TDR-007 §4).
type BankEvent struct {
	EventID        string
	Type           string // one of the Event* constants
	BankReference  string
	IdempotencyKey uuid.UUID // our transaction ID (TDR-005)
	Amount         money.Money
	Status         string // StatusSucceeded or StatusFailed, matching Type
	FailureCode    string // set when Status is StatusFailed
	FailureMessage string
	OccurredAt     time.Time
}

// Succeeded reports whether the bank operation succeeded.
func (e BankEvent) Succeeded() bool { return e.Status == StatusSucceeded }

// EventHandler applies a bank event; payment implements it (wired in cmd/wallet, T016).
type EventHandler interface {
	// HandleBankEvent runs inside the worker's DB transaction: its writes commit only when
	// it returns nil. Return ErrUnknownTransaction when no transaction matches the event, so
	// the event is retried later. It must be idempotent: an event can be handled again after
	// a crash between the commit and the inbox update.
	HandleBankEvent(ctx context.Context, tx pgx.Tx, ev BankEvent) error
}

// ErrUnknownTransaction means no transaction matches the event yet: it may race with the
// outbox commit that stores the bank reference, so the event is retried, and becomes DEAD
// after the last attempt (TDR-007 §3).
var ErrUnknownTransaction = errors.New("webhook: unknown transaction")

// Querier runs SQL: the pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Transactor runs fn in one database transaction (database.DB.InTx).
type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
}

// Results of POST /webhooks/bank, the label of webhook_received_total.
const (
	ReceivedStored           = "stored"
	ReceivedDuplicate        = "duplicate"
	ReceivedInvalidSignature = "invalid_signature"
	ReceivedMalformed        = "malformed"
	ReceivedError            = "error"
)

// Observer receives the webhook metrics and the queue engine's events (queue="inbox").
type Observer interface {
	queue.Observer
	// ObserveWebhookReceived counts one POST /webhooks/bank by result (the Received* values).
	ObserveWebhookReceived(result string)
}

type noopObserver struct{}

func (noopObserver) ObserveQueueClaimed(string, int)               {}
func (noopObserver) ObserveQueueJob(string, string, time.Duration) {}
func (noopObserver) ObserveWebhookReceived(string)                 {}

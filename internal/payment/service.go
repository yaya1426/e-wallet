// Package payment holds the bank-facing money flows: top-up and withdrawal use cases, their
// outbox handlers (the bank calls), and ApplyBankOutcome, the single place where a bank
// result changes a transaction and the ledger, whoever reports it (webhook or
// reconciliation; TDR-007 §3, TDR-009). It is an orchestrator: it owns no table and works
// through the other modules inside one DB transaction per step, and never calls the bank
// inside a DB transaction (TDR-010 §6).
package payment

import (
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// ErrUnsupportedOutcome means the outcome is for a flow this version cannot apply yet
// (payouts until T019). The caller retries it, so it is applied once the flow exists.
var ErrUnsupportedOutcome = errors.New("payment: unsupported bank outcome")

// ErrNotFinal means the outcome is not a final result (the bank operation is still pending).
var ErrNotFinal = errors.New("payment: bank outcome is not final")

// Deps are the payment service's collaborators.
type Deps struct {
	DB            *database.DB
	Identity      *identity.Service
	Wallets       *wallet.Service
	Transactions  *transaction.Service
	Ledger        *ledger.Service
	Discrepancies *discrepancy.Service
	Idempotency   *idempotency.Service
	Outbox        *outbox.Service
	Bank          bank.Gateway
	Limits        wallet.Limits
	Clock         clock.Clock
	Log           *slog.Logger // nil discards
}

// Service is the payment orchestrator.
type Service struct {
	db            *database.DB
	identity      *identity.Service
	wallets       *wallet.Service
	transactions  *transaction.Service
	ledger        *ledger.Service
	discrepancies *discrepancy.Service
	idempotency   *idempotency.Service
	outbox        *outbox.Service
	bank          bank.Gateway
	limits        wallet.Limits
	clock         clock.Clock
	log           *slog.Logger
}

// NewService returns the payment service. Register its outbox handlers (OutboxHandlers)
// before the outbox runs.
func NewService(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		db: d.DB, identity: d.Identity, wallets: d.Wallets, transactions: d.Transactions, ledger: d.Ledger,
		discrepancies: d.Discrepancies, idempotency: d.Idempotency, outbox: d.Outbox, bank: d.Bank,
		limits: d.Limits, clock: d.Clock, log: log,
	}
}

// OutboxHandlers returns the outbox event types payment delivers and their handlers, to
// register with outbox.Service.Register while wiring.
func (s *Service) OutboxHandlers() map[string]outbox.Handler {
	return map[string]outbox.Handler{
		EventTopUpRequested: s.deliverTopUpRequested,
	}
}

// Response is what an HTTP handler sends: a fresh response, or a stored one to replay.
type Response struct {
	Status        int
	Body          any
	Replay        *idempotency.Replay
	TransactionID uuid.UUID // the created transaction; uuid.Nil for a replay or a business error
}

// Renderer turns the outcome of a request (the created transaction, or a business error)
// into the HTTP response to send and to store for idempotent replays.
type Renderer func(transaction.Transaction, error) (status int, body any)

// Package payment holds the bank-facing money flows: top-up and withdrawal use cases, and
// ApplyBankOutcome, the single place where a bank result changes a transaction and the
// ledger, whoever reports it (webhook or reconciliation; TDR-007 §3, TDR-009). It is an
// orchestrator: it owns no table and works through the wallet, transaction, ledger and
// discrepancy modules inside the caller's DB transaction.
package payment

import (
	"errors"
	"log/slog"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// ErrUnsupportedOutcome means the outcome is for a flow this version cannot apply yet
// (payouts until T019). The caller retries it, so it is applied once the flow exists.
var ErrUnsupportedOutcome = errors.New("payment: unsupported bank outcome")

// ErrNotFinal means the outcome is not a final result (the bank operation is still pending).
var ErrNotFinal = errors.New("payment: bank outcome is not final")

// Service is the payment orchestrator.
type Service struct {
	wallets       *wallet.Service
	transactions  *transaction.Service
	ledger        *ledger.Service
	discrepancies *discrepancy.Service
	clock         clock.Clock
	log           *slog.Logger
}

// NewService returns the payment service. log may be nil.
func NewService(wallets *wallet.Service, transactions *transaction.Service, ledgerSvc *ledger.Service,
	discrepancies *discrepancy.Service, clk clock.Clock, log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		wallets: wallets, transactions: transactions, ledger: ledgerSvc, discrepancies: discrepancies,
		clock: clk, log: log,
	}
}

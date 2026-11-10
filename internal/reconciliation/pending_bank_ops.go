package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// JobPendingBankOps is the name of the lost-webhook job (TDR-009 §2).
const JobPendingBankOps = "pending_bank_ops"

// maxReportedErrors bounds the errors joined into a failed run's error.
const maxReportedErrors = 5

// Deps are what the reconciliation jobs work with.
type Deps struct {
	DB            *database.DB
	Transactions  *transaction.Service
	Payment       *payment.Service
	Discrepancies *discrepancy.Service
	Outbox        *outbox.Service // dead_outbox
	Bank          bank.Gateway
	Clock         clock.Clock
	Log           *slog.Logger  // nil discards
	Stuck         StuckObserver // stuck_alert's gauge; nil discards
}

// StuckObserver receives the number of stuck transactions per type (TOPUP, WITHDRAWAL).
type StuckObserver interface {
	ObserveStuckTransactions(txnType string, n int64)
}

// jobTimeout is a job's timeout for an interval: interval − 5 s, at least 1 s, at most the
// interval.
func jobTimeout(interval time.Duration) time.Duration {
	return min(max(interval-5*time.Second, time.Second), interval)
}

// PendingBankOpsConfig are the job's thresholds (RECON_PENDING_*).
type PendingBankOpsConfig struct {
	Interval time.Duration // how often it runs
	MinAge   time.Duration // a transaction must be older than this (by created_at)
	Recheck  time.Duration // and not checked within this
	Batch    int           // at most this many per run
}

// PendingBankOps returns the job that heals lost webhooks (TDR-009 §2, PRD-002 TU-BR-6,
// PRD-002/003 §7): it asks the bank about top-ups and withdrawals still PROCESSING after
// MinAge and applies a final answer through payment.ApplyBankOutcome, the same code the
// webhook uses. Its timeout is Interval − 5 s (at least 1 s, at most Interval).
func PendingBankOps(d Deps, cfg PendingBankOpsConfig) Job {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	timeout := jobTimeout(cfg.Interval)
	p := pendingBankOps{deps: d, cfg: cfg, log: d.Log.With(slog.String("job", JobPendingBankOps))}
	return Job{Name: JobPendingBankOps, Interval: cfg.Interval, Timeout: timeout, Run: p.run}
}

type pendingBankOps struct {
	deps Deps
	cfg  PendingBankOpsConfig
	log  *slog.Logger
}

// Results of checking one transaction, for the logs.
const (
	resultApplied      = "applied"
	resultStillPending = "still_pending"
	resultNotFound     = "not_found"
	resultError        = "error"
)

// run checks one batch, oldest first, one transaction at a time; no DB transaction is open
// during a bank call (TDR-010 §6). A bank call that fails is skipped and retried next run.
// The run is FAILED only when every bank call of a non-empty batch failed (the bank is down).
func (p pendingBankOps) run(ctx context.Context) Result {
	now := p.deps.Clock.Now()
	txns, err := p.deps.Transactions.ListForReconciliation(ctx, p.deps.DB.Pool(),
		now.Add(-p.cfg.MinAge), now.Add(-p.cfg.Recheck), p.cfg.Batch)
	if err != nil {
		return Result{Err: err}
	}
	var (
		res         Result
		calls       int
		failedCalls int
		errs        []error
	)
	for _, txn := range txns {
		if ctx.Err() != nil {
			break // timeout or shutdown: the rest is picked up next run
		}
		calls++
		result, fixed, err := p.check(ctx, txn)
		log := p.log.With(slog.String("transaction_id", txn.ID.String()), slog.String("type", string(txn.Type)),
			slog.String("result", result))
		if err != nil {
			if result == resultError {
				failedCalls++
			}
			if len(errs) < maxReportedErrors {
				errs = append(errs, fmt.Errorf("transaction %s: %w", txn.ID, err))
			}
			log.WarnContext(ctx, "reconciliation check failed; will retry", slog.Any("error", err))
			continue
		}
		res.Checked++
		if fixed {
			res.Fixed++
		}
		log.InfoContext(ctx, "reconciliation checked a transaction", slog.Bool("fixed", fixed))
	}
	if calls > 0 && failedCalls == calls {
		res.Err = errors.Join(errs...)
	}
	return res
}

// check asks the bank about one PROCESSING transaction and acts on the answer. fixed reports
// whether it reached a final status. A failed bank call returns resultError with the error.
func (p pendingBankOps) check(ctx context.Context, txn transaction.Transaction) (result string, fixed bool, err error) {
	op, err := p.deps.Bank.GetOperation(ctx, txn.ID) // the idempotency key is the transaction ID
	switch {
	case errors.Is(err, bank.ErrNotFound):
		return resultNotFound, false, p.notFound(ctx, txn)
	case err != nil:
		return resultError, false, err
	case op.Status == bank.StatusPending:
		return resultStillPending, false, p.deps.Transactions.MarkReconciled(ctx, p.deps.DB.Pool(), txn.ID, p.deps.Clock.Now())
	}
	err = p.deps.DB.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := p.deps.Payment.ApplyBankOutcome(ctx, tx, txn.ID, op.Outcome()); err != nil {
			return err
		}
		after, err := p.deps.Transactions.Get(ctx, tx, txn.ID)
		if err != nil {
			return err
		}
		fixed = after.Status.Terminal()
		return p.deps.Transactions.MarkReconciled(ctx, tx, txn.ID, p.deps.Clock.Now())
	})
	if err != nil {
		return resultApplied, false, fmt.Errorf("apply bank outcome: %w", err)
	}
	return resultApplied, fixed, nil
}

// notFound handles a bank that has no operation for a transaction it accepted: a
// contradiction, never auto-fixed (TDR-009 §3, decided 2026-11-10). A CRITICAL
// STATUS_MISMATCH is recorded (once while open) and nothing else changes.
func (p pendingBankOps) notFound(ctx context.Context, txn transaction.Transaction) error {
	return p.deps.DB.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		locked, err := p.deps.Transactions.GetForUpdate(ctx, tx, txn.ID)
		if err != nil {
			return err
		}
		if locked.Status == transaction.StatusProcessing {
			if err := p.deps.Discrepancies.Record(ctx, tx, discrepancy.New{
				Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityCritical, TransactionID: txn.ID,
				Details: map[string]any{
					"reason":                     "the bank has no operation for a PROCESSING transaction",
					"source":                     JobPendingBankOps,
					"transaction_type":           locked.Type,
					"transaction_amount_minor":   locked.Amount.Amount(),
					"transaction_bank_reference": locked.BankReference,
				},
			}); err != nil {
				return err
			}
		}
		return p.deps.Transactions.MarkReconciled(ctx, tx, txn.ID, p.deps.Clock.Now())
	})
}

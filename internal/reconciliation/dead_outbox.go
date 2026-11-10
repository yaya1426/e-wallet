package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// JobDeadOutbox is the name of the dead-request job (TDR-009 §2).
const JobDeadOutbox = "dead_outbox"

// DeadOutboxConfig are the job's settings (RECON_DEAD_*).
type DeadOutboxConfig struct {
	Interval time.Duration
	Batch    int
}

// deadRequestTypes are the outbox events that ask the bank to move money.
var deadRequestTypes = []string{payment.EventTopUpRequested, payment.EventWithdrawalRequested}

// DeadOutbox returns the job that resolves top-ups and withdrawals left PENDING because their
// bank request went DEAD in the outbox (TDR-009 §2, PRD-002 TU-BR-7, PRD-003 §7). It asks the
// bank: no record → FAILED with BANK_UNAVAILABLE (a withdrawal is refunded); a record → carry
// on from what the bank knows. Each handled event is resolved in the outbox in the same
// transaction as the change.
func DeadOutbox(d Deps, cfg DeadOutboxConfig) Job {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	j := deadOutbox{deps: d, cfg: cfg, log: d.Log.With(slog.String("job", JobDeadOutbox))}
	return Job{Name: JobDeadOutbox, Interval: cfg.Interval, Timeout: jobTimeout(cfg.Interval), Run: j.run}
}

type deadOutbox struct {
	deps Deps
	cfg  DeadOutboxConfig
	log  *slog.Logger
}

// run handles one batch of dead events, one at a time, with no DB transaction open during a
// bank call (TDR-010 §6). A failed bank call leaves the event DEAD for the next run; the run
// is FAILED only when every bank call of a non-empty batch failed.
func (j deadOutbox) run(ctx context.Context) Result {
	events, err := j.deps.Outbox.ListDead(ctx, j.deps.DB.Pool(), deadRequestTypes, j.cfg.Batch)
	if err != nil {
		return Result{Err: err}
	}
	var (
		res                Result
		calls, failedCalls int
		errs               []error
	)
	for _, ev := range events {
		if ctx.Err() != nil {
			break
		}
		outcome, fixed, called, err := j.handle(ctx, ev)
		if called {
			calls++
		}
		log := j.log.With(slog.String("event_id", ev.ID.String()), slog.String("transaction_id", ev.AggregateID.String()),
			slog.String("result", outcome))
		if err != nil {
			if outcome == resultError {
				failedCalls++
			}
			if len(errs) < maxReportedErrors {
				errs = append(errs, fmt.Errorf("event %s: %w", ev.ID, err))
			}
			log.WarnContext(ctx, "dead request not resolved; will retry", slog.Any("error", err))
			continue
		}
		res.Checked++
		if fixed {
			res.Fixed++
		}
		log.InfoContext(ctx, "dead request resolved", slog.Bool("fixed", fixed))
	}
	if calls > 0 && failedCalls == calls {
		res.Err = errors.Join(errs...)
	}
	return res
}

// Outcomes of handling one dead event, for the logs.
const (
	resultMissing      = "transaction_missing"
	resultAlreadyMoved = "already_moved_on"
	resultFailed       = "failed_bank_unavailable"
	resultAdopted      = "adopted_pending"
)

// handle resolves one dead event. called reports whether the bank was asked.
func (j deadOutbox) handle(ctx context.Context, ev outbox.DeadEvent) (outcome string, fixed, called bool, err error) {
	txn, err := j.deps.Transactions.Get(ctx, j.deps.DB.Pool(), ev.AggregateID)
	if errors.Is(err, transaction.ErrNotFound) {
		j.log.ErrorContext(ctx, "dead request for a missing transaction", slog.String("event_id", ev.ID.String()))
		return resultMissing, false, false, j.resolveOnly(ctx, ev, "transaction not found")
	}
	if err != nil {
		return resultError, false, false, err
	}
	if txn.Status != transaction.StatusPending {
		return resultAlreadyMoved, false, false, j.resolveOnly(ctx, ev, "already "+string(txn.Status))
	}

	op, err := j.deps.Bank.GetOperation(ctx, txn.ID)
	switch {
	case errors.Is(err, bank.ErrNotFound):
		moved := false
		err = j.inTx(ctx, txn, ev, func(ctx context.Context, tx pgx.Tx) (string, error) {
			err := j.deps.Payment.FailUnreachable(ctx, tx, txn.ID)
			if errors.Is(err, transaction.ErrStatusConflict) {
				moved = true
				return "already moved on", nil
			}
			return "bank has no record; " + payment.CodeBankUnavailable, err
		})
		if moved {
			return resultAlreadyMoved, false, true, err
		}
		return resultFailed, err == nil, true, err
	case err != nil:
		return resultError, false, true, err
	case op.Status == bank.StatusPending:
		err = j.inTx(ctx, txn, ev, func(ctx context.Context, tx pgx.Tx) (string, error) {
			locked, err := j.deps.Transactions.GetForUpdate(ctx, tx, txn.ID)
			if err != nil {
				return "", err
			}
			if locked.Status != transaction.StatusPending {
				return "already " + string(locked.Status), nil
			}
			ref := op.BankReference
			_, err = j.deps.Transactions.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing,
				transaction.Patch{BankReference: &ref})
			return "bank has it; PROCESSING", err
		})
		return resultAdopted, false, true, err
	default:
		err = j.inTx(ctx, txn, ev, func(ctx context.Context, tx pgx.Tx) (string, error) {
			if err := j.deps.Payment.ApplyBankOutcome(ctx, tx, txn.ID, op.Outcome()); err != nil {
				return "", err
			}
			after, err := j.deps.Transactions.Get(ctx, tx, txn.ID)
			if err != nil {
				return "", err
			}
			fixed = after.Status.Terminal()
			return "bank answered " + string(op.Status), nil
		})
		return resultApplied, fixed && err == nil, true, err
	}
}

// inTx runs change, then resolves the event with the note change returned and marks the
// transaction reconciled, all in one DB transaction.
func (j deadOutbox) inTx(ctx context.Context, txn transaction.Transaction, ev outbox.DeadEvent,
	change func(ctx context.Context, tx pgx.Tx) (note string, err error),
) error {
	return j.deps.DB.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		note, err := change(ctx, tx)
		if err != nil {
			return err
		}
		if err := j.deps.Outbox.Resolve(ctx, tx, ev.ID, note); err != nil {
			return err
		}
		return j.deps.Transactions.MarkReconciled(ctx, tx, txn.ID, j.deps.Clock.Now())
	})
}

// resolveOnly resolves an event that needs no business change.
func (j deadOutbox) resolveOnly(ctx context.Context, ev outbox.DeadEvent, note string) error {
	return j.deps.DB.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return j.deps.Outbox.Resolve(ctx, tx, ev.ID, note)
	})
}

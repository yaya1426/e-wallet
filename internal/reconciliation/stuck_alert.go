package reconciliation

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// JobStuckAlert is the name of the stuck-transaction job (TDR-009 §2).
const JobStuckAlert = "stuck_alert"

// stuckListLimit bounds the transactions flagged per run; the gauge counts all of them.
const stuckListLimit = 1000

// StuckAlertConfig are the job's settings (RECON_STUCK_*).
type StuckAlertConfig struct {
	Interval time.Duration
	Age      time.Duration // not final this long after creation → stuck
}

// StuckAlert returns the job that flags top-ups and withdrawals still not final after Age as
// a STUCK_TRANSACTION discrepancy for an operator (TDR-009 §2, PRD-003 §7) and reports their
// number per type to the gauge. It never changes money or status: a stuck withdrawal stays
// reserved.
func StuckAlert(d Deps, cfg StuckAlertConfig) Job {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	j := stuckAlert{deps: d, cfg: cfg, log: d.Log.With(slog.String("job", JobStuckAlert))}
	return Job{Name: JobStuckAlert, Interval: cfg.Interval, Timeout: jobTimeout(cfg.Interval), Run: j.run}
}

type stuckAlert struct {
	deps Deps
	cfg  StuckAlertConfig
	log  *slog.Logger
}

func (j stuckAlert) run(ctx context.Context) Result {
	now := j.deps.Clock.Now()
	before := now.Add(-j.cfg.Age)
	txns, err := j.deps.Transactions.ListStuck(ctx, j.deps.DB.Pool(), before, stuckListLimit)
	if err != nil {
		return Result{Err: err}
	}
	var res Result
	for _, txn := range txns {
		if ctx.Err() != nil {
			break
		}
		if err := j.flag(ctx, txn, now); err != nil {
			return Result{Checked: res.Checked, Err: err}
		}
		res.Checked++
	}
	counts, err := j.deps.Transactions.CountStuck(ctx, j.deps.DB.Pool(), before)
	if err != nil {
		return Result{Checked: res.Checked, Err: err}
	}
	var total int64
	for typ, n := range counts {
		total += n
		if j.deps.Stuck != nil {
			j.deps.Stuck.ObserveStuckTransactions(string(typ), n)
		}
	}
	if total > 0 {
		j.log.WarnContext(ctx, "transactions stuck for an operator", slog.Int64("stuck", total),
			slog.Int64("topups", counts[transaction.TypeTopUp]), slog.Int64("withdrawals", counts[transaction.TypeWithdrawal]))
	}
	return res
}

// flag records STUCK_TRANSACTION for txn (one open row per transaction): CRITICAL for a
// withdrawal, whose money is reserved, WARNING for a top-up (decided 2026-11-10).
func (j stuckAlert) flag(ctx context.Context, txn transaction.Transaction, now time.Time) error {
	severity := discrepancy.SeverityWarning
	if txn.Type == transaction.TypeWithdrawal {
		severity = discrepancy.SeverityCritical
	}
	return j.deps.DB.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return j.deps.Discrepancies.Record(ctx, tx, discrepancy.New{
			Kind: discrepancy.KindStuckTransaction, Severity: severity, TransactionID: txn.ID,
			Details: map[string]any{
				"type": txn.Type, "status": txn.Status, "amount_minor": txn.Amount.Amount(),
				"bank_reference": txn.BankReference, "created_at": txn.CreatedAt.UTC(),
				"age_hours": int64(now.Sub(txn.CreatedAt).Hours()),
			},
		})
	})
}

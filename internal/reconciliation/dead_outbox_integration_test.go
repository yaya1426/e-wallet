//go:build integration

package reconciliation_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banktest"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/reconciliation"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

var deadCfg = reconciliation.DeadOutboxConfig{Interval: 5 * time.Minute, Batch: 100}

func (f reconFixture) runDead(t *testing.T) reconciliation.Result {
	t.Helper()
	return reconciliation.DeadOutbox(f.deps, deadCfg).Run(context.Background())
}

type eventState struct {
	status    string
	lastError string
}

func (f reconFixture) event(t *testing.T, txnID uuid.UUID) eventState {
	t.Helper()
	var s eventState
	var lastErr *string
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT status, last_error FROM outbox_events WHERE aggregate_id = $1`, txnID).Scan(&s.status, &lastErr))
	if lastErr != nil {
		s.lastError = *lastErr
	}
	return s
}

// deadRequest is a PENDING request whose bank call went DEAD.
func (f reconFixture) deadRequest(t *testing.T, typ transaction.Type, minor int64) transaction.Transaction {
	t.Helper()
	txn := f.pending(t, typ, minor)
	f.killRequests(t)
	require.Equal(t, "DEAD", f.event(t, txn.ID).status)
	return txn
}

// AC-1 and AC-2: the bank has no record → FAILED BANK_UNAVAILABLE (+ refund); event resolved.
func TestDeadOutbox_BankHasNoRecord(t *testing.T) {
	for _, tt := range []struct {
		typ     transaction.Type
		entries []string
		balance int64
	}{
		{transaction.TypeTopUp, []string{}, 0},
		{transaction.TypeWithdrawal, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, 20_000},
	} {
		t.Run(string(tt.typ), func(t *testing.T) {
			f := newReconFixture(t, defaultCfg)
			txn := f.deadRequest(t, tt.typ, 20_000)

			assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.runDead(t))

			got := f.get(t, txn.ID)
			assert.Equal(t, transaction.StatusFailed, got.Status)
			assert.Equal(t, payment.CodeBankUnavailable, got.FailureCode)
			assert.Equal(t, "The bank could not be reached. No money was moved.", got.FailureMessage)
			assert.NotNil(t, got.LastReconciledAt)
			assert.Equal(t, tt.entries, f.entries(t, txn.ID))
			assert.Equal(t, tt.balance, f.balance(t))
			ev := f.event(t, txn.ID)
			assert.Equal(t, "DONE", ev.status)
			assert.Equal(t, "resolved by reconciliation: bank has no record; BANK_UNAVAILABLE", ev.lastError)
			assert.Empty(t, f.openDiscrepancies(t))
		})
	}
}

// AC-3: the bank has it, still pending → PROCESSING with its reference; pending_bank_ops then
// completes it.
func TestDeadOutbox_BankHasItPending(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.deadRequest(t, transaction.TypeTopUp, 30_000)
	f.bank.SetOperation(bank.Operation{IdempotencyKey: txn.ID, BankReference: "BSIM-LATE", Kind: bank.KindDebit,
		Status: bank.StatusPending, Amount: egp(t, 30_000)})

	assert.Equal(t, reconciliation.Result{Checked: 1}, f.runDead(t))
	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, got.Status)
	assert.Equal(t, "BSIM-LATE", got.BankReference)
	assert.Equal(t, "resolved by reconciliation: bank has it; PROCESSING", f.event(t, txn.ID).lastError)

	f.bank.SetOperation(bank.Operation{IdempotencyKey: txn.ID, BankReference: "BSIM-LATE", Kind: bank.KindDebit,
		Status: bank.StatusSucceeded, Amount: egp(t, 30_000)})
	f.clock.Advance(11 * time.Minute)
	assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.run(t), "pending_bank_ops takes over")
	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(30_000), f.balance(t))
}

// AC-4: the bank already has a final answer → applied through ApplyBankOutcome.
func TestDeadOutbox_BankHasFinalAnswer(t *testing.T) {
	t.Run("top-up succeeded", func(t *testing.T) {
		f := newReconFixture(t, defaultCfg)
		txn := f.deadRequest(t, transaction.TypeTopUp, 30_000)
		f.bank.SetOperation(bank.Operation{IdempotencyKey: txn.ID, BankReference: "BSIM-1", Kind: bank.KindDebit,
			Status: bank.StatusSucceeded, Amount: egp(t, 30_000)})

		assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.runDead(t))
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusCompleted, got.Status)
		assert.Equal(t, "BSIM-1", got.BankReference)
		assert.Equal(t, int64(30_000), f.balance(t))
		assert.Equal(t, "resolved by reconciliation: bank answered SUCCEEDED", f.event(t, txn.ID).lastError)
	})
	t.Run("withdrawal failed", func(t *testing.T) {
		f := newReconFixture(t, defaultCfg)
		txn := f.deadRequest(t, transaction.TypeWithdrawal, 20_000)
		f.bank.SetOperation(bank.Operation{IdempotencyKey: txn.ID, BankReference: "BSIM-2", Kind: bank.KindPayout,
			Status: bank.StatusFailed, Amount: egp(t, 20_000), FailureCode: "ACCOUNT_CLOSED", FailureMessage: "closed"})

		assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.runDead(t))
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusFailed, got.Status)
		assert.Equal(t, "ACCOUNT_CLOSED", got.FailureCode, "the bank's code, not BANK_UNAVAILABLE")
		assert.Equal(t, int64(20_000), f.balance(t), "refunded")
	})
}

// AC-5: the transaction already moved on → nothing changes, the event is resolved.
func TestDeadOutbox_AlreadyMovedOn(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.deadRequest(t, transaction.TypeTopUp, 30_000)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.ApplyBankOutcome(ctx, tx, txn.ID, bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded,
			BankReference: "BSIM-WEBHOOK", Amount: egp(t, 30_000)})
	}))

	assert.Equal(t, reconciliation.Result{Checked: 1}, f.runDead(t))
	assert.Equal(t, "resolved by reconciliation: already COMPLETED", f.event(t, txn.ID).lastError)
	assert.Empty(t, f.bank.Calls(), "no bank call needed")
	assert.Equal(t, int64(30_000), f.balance(t), "credited once")
}

// AC-6: a temporary error leaves the event DEAD for the next run; all failing → run FAILED.
func TestDeadOutbox_TemporaryErrors(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	flaky := f.pending(t, transaction.TypeTopUp, 10_000)
	fine := f.pending(t, transaction.TypeTopUp, 20_000)
	f.killRequests(t)
	f.bank.Script(flaky.ID, banktest.Response{Err: bank.ErrTemporary})

	res := f.runDead(t)
	assert.NoError(t, res.Err)
	assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, res)
	assert.Equal(t, "DEAD", f.event(t, flaky.ID).status)
	assert.Equal(t, transaction.StatusPending, f.get(t, flaky.ID).Status)
	assert.Equal(t, transaction.StatusFailed, f.get(t, fine.ID).Status)

	assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.runDead(t), "retried")
	assert.Equal(t, "DONE", f.event(t, flaky.ID).status)

	f2 := newReconFixture(t, defaultCfg)
	a := f2.deadRequest(t, transaction.TypeTopUp, 10_000)
	f2.bank.Script(a.ID, banktest.Response{Err: bank.ErrTemporary})
	res = f2.runDead(t)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "temporary")
}

// AC-7: a resolved event is not picked again, and two concurrent runs change it once.
func TestDeadOutbox_ResolvedOnce(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.deadRequest(t, transaction.TypeWithdrawal, 20_000)

	results := make([]reconciliation.Result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.runDead(t) })
	}
	wg.Wait()
	assert.Equal(t, 1, results[0].Fixed+results[1].Fixed, "changed once")
	assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, f.entries(t, txn.ID))
	assert.Equal(t, reconciliation.Result{}, f.runDead(t), "nothing left")
}

// AC-8: dead events of other types are left alone.
func TestDeadOutbox_OnlyRequestEvents(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	f.outbox.Register("something.else", nil2Retry)
	other := uuid.New()
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.outbox.Enqueue(ctx, tx, outbox.Event{AggregateType: "x", AggregateID: other, Type: "something.else", Payload: 1})
	}))
	f.killRequests(t)
	assert.Equal(t, reconciliation.Result{}, f.runDead(t))
	assert.Equal(t, "DEAD", f.event(t, other).status)
}

// --- stuck_alert ---

type stuckGauge struct {
	mu sync.Mutex
	n  map[string]int64
}

func (g *stuckGauge) ObserveStuckTransactions(typ string, n int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n[typ] = n
}

func (g *stuckGauge) get(typ string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n[typ]
}

// AC-9.
func TestStuckAlert(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	gauge := &stuckGauge{n: map[string]int64{}}
	deps := f.deps
	deps.Stuck = gauge
	job := reconciliation.StuckAlert(deps, reconciliation.StuckAlertConfig{Interval: 15 * time.Minute, Age: 24 * time.Hour})

	stuckTopUp := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-T")
	stuckWithdrawal := f.pending(t, transaction.TypeWithdrawal, 20_000) // funding top-up completes: not stuck
	done := f.processing(t, transaction.TypeTopUp, 5_000, "BSIM-D")
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.ApplyBankOutcome(ctx, tx, done.ID, bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded,
			BankReference: "BSIM-D", Amount: egp(t, 5_000)})
	}))
	f.clock.Advance(25 * time.Hour)
	young := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-Y")
	balanceBefore := f.balance(t)

	for range 2 {
		res := job.Run(context.Background())
		require.NoError(t, res.Err)
		assert.Equal(t, 2, res.Checked)
		assert.Zero(t, res.Fixed)
	}
	list := f.openDiscrepancies(t)
	require.Len(t, list, 2, "one open row each, even after two runs")
	byTxn := map[uuid.UUID]discrepancy.Discrepancy{}
	for _, d := range list {
		byTxn[d.TransactionID] = d
		assert.Equal(t, discrepancy.KindStuckTransaction, d.Kind)
	}
	assert.Equal(t, discrepancy.SeverityWarning, byTxn[stuckTopUp.ID].Severity)
	assert.Equal(t, discrepancy.SeverityCritical, byTxn[stuckWithdrawal.ID].Severity)
	var details map[string]any
	require.NoError(t, json.Unmarshal(byTxn[stuckWithdrawal.ID].Details, &details))
	assert.Equal(t, "PENDING", details["status"])
	assert.EqualValues(t, 25, details["age_hours"])
	assert.NotContains(t, byTxn, young.ID)
	assert.Equal(t, int64(1), gauge.get("TOPUP"))
	assert.Equal(t, int64(1), gauge.get("WITHDRAWAL"))
	assert.Equal(t, transaction.StatusPending, f.get(t, stuckWithdrawal.ID).Status, "nothing changes")
	assert.Equal(t, balanceBefore, f.balance(t), "money stays reserved")

	// Once they are final, the gauge returns to 0 (the alert clears).
	for _, txn := range []transaction.Transaction{stuckTopUp, stuckWithdrawal} {
		require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			if txn.Status == transaction.StatusPending {
				return f.payment.FailUnreachable(ctx, tx, txn.ID)
			}
			return f.payment.ApplyBankOutcome(ctx, tx, txn.ID, bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded,
				BankReference: "BSIM-T", Amount: egp(t, 10_000)})
		}))
	}
	require.NoError(t, job.Run(context.Background()).Err)
	assert.Zero(t, gauge.get("TOPUP"))
	assert.Zero(t, gauge.get("WITHDRAWAL"))
}

func nil2Retry(context.Context, queue.Job) queue.Result {
	return queue.Retry(errors.New("unreachable"))
}

//go:build integration

package reconciliation_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/yaya1426/e-wallet/internal/ledger/ledgertest"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/payment/paymenttest"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/reconciliation"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

var defaultCfg = reconciliation.PendingBankOpsConfig{Interval: time.Minute, MinAge: 10 * time.Minute, Recheck: 5 * time.Minute, Batch: 100}

type reconFixture struct {
	db            *database.DB
	clock         *clocktest.Fake
	bank          *banktest.Fake
	ledger        *ledger.Service
	txns          *transaction.Service
	discrepancies *discrepancy.Service
	payment       *payment.Service
	outbox        *outbox.Service
	deps          reconciliation.Deps
	user          uuid.UUID
	wallet        wallet.Wallet
	job           reconciliation.Job
}

func newReconFixture(t *testing.T, cfg reconciliation.PendingBankOpsConfig) reconFixture {
	t.Helper()
	db := setup(t)
	t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })         // AC-10
	t.Cleanup(func() { paymenttest.AssertInTransit(t, db.Pool()) }) // AC-10
	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	f := reconFixture{db: db, clock: clk, bank: banktest.New(), ledger: ledger.NewService(db, clk), txns: transaction.NewService(clk)}
	wallets := wallet.NewService(db, f.ledger, clk)
	f.discrepancies = discrepancy.NewService(db.Pool(), clk, nil)
	f.payment = payment.NewService(payment.Deps{DB: db, Wallets: wallets, Transactions: f.txns, Ledger: f.ledger,
		Discrepancies: f.discrepancies, Clock: clk})
	// The request events can only go DEAD: delivery always fails, after one attempt.
	var err error
	f.outbox, err = outbox.NewService(db.Pool(), clk, outbox.Config{Workers: 1, MaxAttempts: 1, PollInterval: 10 * time.Millisecond}, nil, nil)
	require.NoError(t, err)
	for _, typ := range []string{payment.EventTopUpRequested, payment.EventWithdrawalRequested} {
		f.outbox.Register(typ, func(context.Context, queue.Job) queue.Result { return queue.Retry(errors.New("bank unreachable")) })
	}
	f.deps = reconciliation.Deps{DB: db, Transactions: f.txns, Payment: f.payment, Discrepancies: f.discrepancies,
		Outbox: f.outbox, Bank: f.bank, Clock: clk}
	f.job = reconciliation.PendingBankOps(f.deps, cfg)
	f.user = uuid.New()
	require.NoError(t, db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
			VALUES ($1, '+201000000001', 'Test User', 'x', now())`, f.user); err != nil {
			return err
		}
		id, err := wallets.Open(ctx, tx, f.user)
		if err != nil {
			return err
		}
		f.wallet, err = wallets.GetByID(ctx, tx, id)
		return err
	}))
	return f
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(minor, money.EGP)
	require.NoError(t, err)
	return m
}

// pending creates a PENDING top-up or (funded, reserved) withdrawal at the fake clock's time,
// with its bank request event in the outbox, as RequestTopUp / RequestWithdrawal do.
func (f reconFixture) pending(t *testing.T, typ transaction.Type, minor int64) transaction.Transaction {
	t.Helper()
	if typ == transaction.TypeWithdrawal {
		f.fund(t, minor)
	}
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		n := transaction.New{Type: typ, Status: transaction.StatusPending, InitiatorUserID: f.user,
			Amount: egp(t, minor), IdempotencyKey: uuid.NewString(),
			BankAccount: &transaction.BankAccountSnapshot{IBAN: "EG890003000100000000123450000", HolderName: "Test User"}}
		event := payment.EventTopUpRequested
		if typ == transaction.TypeTopUp {
			n.DestinationWalletID = f.wallet.ID
		} else {
			n.SourceWalletID = f.wallet.ID
			event = payment.EventWithdrawalRequested
		}
		var err error
		if txn, err = f.txns.Create(ctx, tx, n); err != nil {
			return err
		}
		if typ == transaction.TypeWithdrawal {
			inTransit, err := f.ledger.GetAccountByCode(ctx, tx, ledger.CodeWithdrawalsInTransit)
			if err != nil {
				return err
			}
			if _, err := f.ledger.Post(ctx, tx, ledger.Entry{TransactionID: txn.ID, Type: ledger.EntryWithdrawalReserve,
				Postings: []ledger.Posting{
					{AccountID: f.wallet.LedgerAccountID, Direction: ledger.Debit, Amount: egp(t, minor)},
					{AccountID: inTransit.ID, Direction: ledger.Credit, Amount: egp(t, minor)},
				}}); err != nil {
				return err
			}
		}
		return f.outbox.Enqueue(ctx, tx, outbox.Event{AggregateType: "transaction", AggregateID: txn.ID, Type: event,
			Payload: map[string]any{"transaction_id": txn.ID}})
	}))
	return txn
}

// processing creates a top-up or (funded, reserved) withdrawal moved to PROCESSING with ref,
// at the fake clock's current time. Its request event is resolved as delivered.
func (f reconFixture) processing(t *testing.T, typ transaction.Type, minor int64, ref string) transaction.Transaction {
	t.Helper()
	txn := f.pending(t, typ, minor)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing,
			transaction.Patch{BankReference: &ref})
		if err != nil {
			return err
		}
		// The outbox delivered the request (here: marked so, since the test outbox only fails).
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET status = 'DONE' WHERE aggregate_id = $1`, txn.ID)
		return err
	}))
	return txn
}

// killRequests runs the test outbox until every request event is DEAD.
func (f reconFixture) killRequests(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.outbox.Run(ctx) }()
	require.Eventually(t, func() bool {
		var open int
		require.NoError(t, f.db.Pool().QueryRow(context.Background(),
			`SELECT count(*) FROM outbox_events WHERE status IN ('PENDING', 'PROCESSING')`).Scan(&open))
		return open == 0
	}, 10*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

// fund credits the wallet through a completed top-up.
func (f reconFixture) fund(t *testing.T, minor int64) {
	t.Helper()
	ref := "FUND-" + uuid.NewString()
	txn := f.processing(t, transaction.TypeTopUp, minor, ref)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.ApplyBankOutcome(ctx, tx, txn.ID, bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded,
			BankReference: ref, Amount: egp(t, minor)})
	}))
}

// bankKnows tells the fake bank about the operation of txn.
func (f reconFixture) bankKnows(t *testing.T, txn transaction.Transaction, status bank.OperationStatus, minor int64) {
	t.Helper()
	kind := bank.KindDebit
	if txn.Type == transaction.TypeWithdrawal {
		kind = bank.KindPayout
	}
	op := bank.Operation{IdempotencyKey: txn.ID, BankReference: txn.BankReference, Kind: kind, Status: status, Amount: egp(t, minor)}
	if status == bank.StatusFailed {
		op.FailureCode, op.FailureMessage = "ACCOUNT_CLOSED", "The account is closed."
		if kind == bank.KindDebit {
			op.FailureCode, op.FailureMessage = "INSUFFICIENT_FUNDS", "Insufficient funds."
		}
	}
	f.bank.SetOperation(op)
}

func (f reconFixture) run(t *testing.T) reconciliation.Result {
	t.Helper()
	return f.job.Run(context.Background())
}

func (f reconFixture) get(t *testing.T, id uuid.UUID) transaction.Transaction {
	t.Helper()
	txn, err := f.txns.Get(context.Background(), f.db.Pool(), id)
	require.NoError(t, err)
	return txn
}

func (f reconFixture) entries(t *testing.T, id uuid.UUID) []string {
	t.Helper()
	rows, err := f.db.Pool().Query(context.Background(), `SELECT entry_type FROM journal_entries WHERE transaction_id = $1 ORDER BY created_at`, id)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return out
}

func (f reconFixture) balance(t *testing.T) int64 {
	t.Helper()
	b, err := f.ledger.Balance(context.Background(), f.db.Pool(), f.wallet.LedgerAccountID)
	require.NoError(t, err)
	return b.Amount()
}

func (f reconFixture) calls(key uuid.UUID) int {
	n := 0
	for _, c := range f.bank.Calls() {
		if c.Op == "GetOperation" && c.Key == key {
			n++
		}
	}
	return n
}

func (f reconFixture) openDiscrepancies(t *testing.T) []discrepancy.Discrepancy {
	t.Helper()
	list, err := f.discrepancies.List(context.Background(), discrepancy.Filter{Status: discrepancy.StatusOpen})
	require.NoError(t, err)
	return list
}

// AC-1: a lost debit.succeeded is applied by the job.
func TestPendingBankOps_TopUpSucceeded(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.processing(t, transaction.TypeTopUp, 50_000, "BSIM-1")
	f.bankKnows(t, txn, bank.StatusSucceeded, 50_000)
	f.clock.Advance(11 * time.Minute)

	res := f.run(t)

	assert.NoError(t, res.Err)
	assert.Equal(t, 1, res.Checked)
	assert.Equal(t, 1, res.Fixed)
	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusCompleted, got.Status)
	require.NotNil(t, got.LastReconciledAt)
	assert.True(t, got.LastReconciledAt.Equal(f.clock.Now()))
	assert.Equal(t, []string{string(ledger.EntryTopUpSettle)}, f.entries(t, txn.ID))
	assert.Equal(t, int64(50_000), f.balance(t))
}

// AC-1 and AC-2: the bank's answer for each type, through ApplyBankOutcome.
func TestPendingBankOps_Outcomes(t *testing.T) {
	tests := []struct {
		name    string
		typ     transaction.Type
		bank    bank.OperationStatus
		status  transaction.Status
		code    string
		entries []string
		balance int64
	}{
		{"top-up failed", transaction.TypeTopUp, bank.StatusFailed, transaction.StatusFailed, "INSUFFICIENT_FUNDS", []string{}, 0},
		{"withdrawal paid", transaction.TypeWithdrawal, bank.StatusSucceeded, transaction.StatusCompleted, "",
			[]string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalSettle)}, 0},
		{"withdrawal failed", transaction.TypeWithdrawal, bank.StatusFailed, transaction.StatusFailed, "ACCOUNT_CLOSED",
			[]string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, 20_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReconFixture(t, defaultCfg)
			txn := f.processing(t, tt.typ, 20_000, "BSIM-1")
			f.bankKnows(t, txn, tt.bank, 20_000)
			f.clock.Advance(11 * time.Minute)

			res := f.run(t)

			assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, res)
			got := f.get(t, txn.ID)
			assert.Equal(t, tt.status, got.Status)
			assert.Equal(t, tt.code, got.FailureCode)
			assert.Equal(t, tt.entries, f.entries(t, txn.ID))
			assert.Equal(t, tt.balance, f.balance(t))
		})
	}
}

// AC-3: still pending at the bank → only last_reconciled_at; no new question before RECHECK.
func TestPendingBankOps_StillPending(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.processing(t, transaction.TypeTopUp, 50_000, "BSIM-1")
	f.bankKnows(t, txn, bank.StatusPending, 50_000)
	f.clock.Advance(11 * time.Minute)
	before := f.get(t, txn.ID)

	assert.Equal(t, reconciliation.Result{Checked: 1}, f.run(t))
	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, got.Status)
	require.NotNil(t, got.LastReconciledAt)
	assert.True(t, got.UpdatedAt.Equal(before.UpdatedAt), "bookkeeping does not touch updated_at")
	assert.Equal(t, 1, f.calls(txn.ID))

	f.clock.Advance(4 * time.Minute)
	assert.Equal(t, reconciliation.Result{}, f.run(t), "checked less than RECHECK ago")
	assert.Equal(t, 1, f.calls(txn.ID))

	f.clock.Advance(2 * time.Minute)
	f.bankKnows(t, txn, bank.StatusSucceeded, 50_000)
	assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, f.run(t))
	assert.Equal(t, 2, f.calls(txn.ID))
	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
}

// AC-4: selection — age, status, type and batch size.
func TestPendingBankOps_Selection(t *testing.T) {
	f := newReconFixture(t, reconciliation.PendingBankOpsConfig{Interval: time.Minute, MinAge: 10 * time.Minute, Recheck: 5 * time.Minute, Batch: 2})
	oldest := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-A")
	f.clock.Advance(time.Minute)
	second := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-B")
	f.clock.Advance(time.Minute)
	third := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-C")
	f.clock.Advance(9 * time.Minute) // oldest and second are > 10 min old, third is 9 min old
	young := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-D")
	// A PENDING top-up and a terminal one, both old enough, are never selected.
	var pending transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		pending, err = f.txns.Create(ctx, tx, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending,
			InitiatorUserID: f.user, DestinationWalletID: f.wallet.ID, Amount: egp(t, 10_000), IdempotencyKey: uuid.NewString()})
		return err
	}))
	f.clock.Advance(20 * time.Minute) // now: oldest 31, second 30, third 29, young 20, pending 20 minutes old
	for _, txn := range []transaction.Transaction{oldest, second, third, young} {
		f.bankKnows(t, txn, bank.StatusPending, 10_000)
	}

	assert.Equal(t, 2, f.run(t).Checked, "batch of 2")
	assert.Equal(t, 1, f.calls(oldest.ID))
	assert.Equal(t, 1, f.calls(second.ID))
	assert.Zero(t, f.calls(third.ID), "oldest first")

	assert.Equal(t, 2, f.run(t).Checked)
	assert.Equal(t, 1, f.calls(third.ID))
	assert.Equal(t, 1, f.calls(young.ID))
	assert.Zero(t, f.calls(pending.ID), "PENDING is T023's")

	f2 := newReconFixture(t, defaultCfg)
	fresh := f2.processing(t, transaction.TypeTopUp, 10_000, "BSIM-E")
	f2.clock.Advance(9 * time.Minute)
	assert.Equal(t, reconciliation.Result{}, f2.run(t), "younger than MIN_AGE")
	assert.Zero(t, f2.calls(fresh.ID))
}

// AC-5: the bank has no record of a PROCESSING transaction → one CRITICAL discrepancy.
func TestPendingBankOps_NotFound(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.processing(t, transaction.TypeWithdrawal, 20_000, "BSIM-W")
	f.clock.Advance(11 * time.Minute)

	assert.Equal(t, reconciliation.Result{Checked: 1}, f.run(t))
	f.clock.Advance(6 * time.Minute)
	assert.Equal(t, reconciliation.Result{Checked: 1}, f.run(t))

	list := f.openDiscrepancies(t)
	require.Len(t, list, 1, "one open row")
	assert.Equal(t, discrepancy.KindStatusMismatch, list[0].Kind)
	assert.Equal(t, discrepancy.SeverityCritical, list[0].Severity)
	assert.Equal(t, txn.ID, list[0].TransactionID)
	var details map[string]any
	require.NoError(t, json.Unmarshal(list[0].Details, &details))
	assert.Equal(t, "the bank has no operation for a PROCESSING transaction", details["reason"])
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	assert.Zero(t, f.balance(t), "still reserved")
}

// AC-6: one temporary error does not stop the batch; all errors fail the run.
func TestPendingBankOps_TemporaryErrors(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	flaky := f.processing(t, transaction.TypeTopUp, 10_000, "BSIM-1")
	fine := f.processing(t, transaction.TypeTopUp, 20_000, "BSIM-2")
	f.bankKnows(t, flaky, bank.StatusSucceeded, 10_000)
	f.bankKnows(t, fine, bank.StatusSucceeded, 20_000)
	f.bank.Script(flaky.ID, banktest.Response{Err: bank.ErrTemporary})
	f.clock.Advance(11 * time.Minute)

	res := f.run(t)
	assert.NoError(t, res.Err, "not every call failed")
	assert.Equal(t, 1, res.Checked)
	assert.Equal(t, 1, res.Fixed)
	assert.Equal(t, transaction.StatusCompleted, f.get(t, fine.ID).Status)
	got := f.get(t, flaky.ID)
	assert.Equal(t, transaction.StatusProcessing, got.Status)
	assert.Nil(t, got.LastReconciledAt, "retried next run")

	res = f.run(t)
	assert.Equal(t, reconciliation.Result{Checked: 1, Fixed: 1}, res)
	assert.Equal(t, transaction.StatusCompleted, f.get(t, flaky.ID).Status)

	f2 := newReconFixture(t, defaultCfg)
	a := f2.processing(t, transaction.TypeTopUp, 10_000, "BSIM-A")
	b := f2.processing(t, transaction.TypeTopUp, 10_000, "BSIM-B")
	f2.bank.Script(a.ID, banktest.Response{Err: bank.ErrTemporary})
	f2.bank.Script(b.ID, banktest.Response{Err: bank.ErrTemporary})
	f2.clock.Advance(11 * time.Minute)
	res = f2.run(t)
	require.Error(t, res.Err, "the bank is down")
	assert.Contains(t, res.Err.Error(), a.ID.String())
	assert.Zero(t, res.Checked)
}

// AC-7: the bank's amount differs → AMOUNT_MISMATCH, still PROCESSING.
func TestPendingBankOps_AmountMismatch(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.processing(t, transaction.TypeTopUp, 50_000, "BSIM-1")
	f.bankKnows(t, txn, bank.StatusSucceeded, 49_999)
	f.clock.Advance(11 * time.Minute)

	assert.Equal(t, reconciliation.Result{Checked: 1}, f.run(t))
	list := f.openDiscrepancies(t)
	require.Len(t, list, 1)
	assert.Equal(t, discrepancy.KindAmountMismatch, list[0].Kind)
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	assert.Zero(t, f.balance(t))
}

// AC-8: the webhook got there first (or at the same time): no double posting.
func TestPendingBankOps_WebhookAlreadyApplied(t *testing.T) {
	f := newReconFixture(t, defaultCfg)
	txn := f.processing(t, transaction.TypeTopUp, 50_000, "BSIM-1")
	f.bankKnows(t, txn, bank.StatusSucceeded, 50_000)
	f.clock.Advance(11 * time.Minute)
	// The webhook is applied after the job listed the transaction but before it applies.
	jobDone := make(chan reconciliation.Result, 1)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := f.txns.GetForUpdate(ctx, tx, txn.ID); err != nil { // holds the row like the inbox worker
			return err
		}
		go func() { jobDone <- f.run(t) }()
		time.Sleep(200 * time.Millisecond) // the job is now waiting for the row lock
		return f.payment.ApplyBankOutcome(ctx, tx, txn.ID, bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded,
			BankReference: "BSIM-1", Amount: egp(t, 50_000)})
	}))
	res := <-jobDone
	assert.NoError(t, res.Err)
	assert.Equal(t, 1, res.Checked)
	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
	assert.Len(t, f.entries(t, txn.ID), 1, "credited once")
	assert.Equal(t, int64(50_000), f.balance(t))
	assert.Empty(t, f.openDiscrepancies(t))
}

func TestPendingBankOps_JobShape(t *testing.T) {
	j := reconciliation.PendingBankOps(reconciliation.Deps{}, defaultCfg)
	assert.Equal(t, reconciliation.JobPendingBankOps, j.Name)
	assert.Equal(t, time.Minute, j.Interval)
	assert.Equal(t, 55*time.Second, j.Timeout)
	short := reconciliation.PendingBankOps(reconciliation.Deps{}, reconciliation.PendingBankOpsConfig{Interval: 500 * time.Millisecond})
	assert.Equal(t, 500*time.Millisecond, short.Timeout, "never above the interval")
}

//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/banksim"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/ledger/ledgertest"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/payment/paymenttest"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

const amount = 50_000 // 500.00 EGP

type fixture struct {
	db            *database.DB
	clock         *clocktest.Fake
	ledger        *ledger.Service
	wallets       *wallet.Service
	txns          *transaction.Service
	discrepancies *discrepancy.Service
	payment       *payment.Service
	user          uuid.UUID
	wallet        wallet.Wallet
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))
	t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })  // AC-11
	t.Cleanup(func() { paymenttest.AssertInTransit(t, db) }) // T018 AC-8

	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	f := fixture{db: db, clock: clk, ledger: ledger.NewService(db, clk), txns: transaction.NewService(clk)}
	f.wallets = wallet.NewService(db, f.ledger, clk)
	f.discrepancies = discrepancy.NewService(db.Pool(), clk, nil)
	f.payment = payment.NewService(payment.Deps{
		DB: db, Wallets: f.wallets, Transactions: f.txns, Ledger: f.ledger, Discrepancies: f.discrepancies, Clock: clk,
	})

	// A user row is enough (identity is not under test).
	f.user = uuid.New()
	require.NoError(t, db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
			VALUES ($1, '+201000000001', 'Test User', 'x', now())`, f.user); err != nil {
			return err
		}
		walletID, err := f.wallets.Open(ctx, tx, f.user)
		if err != nil {
			return err
		}
		f.wallet, err = f.wallets.GetByID(ctx, tx, walletID)
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

// topUp creates a top-up of minor piasters; with a bank reference it is moved to PROCESSING
// (what the outbox worker does once the bank accepted the debit, T017).
func (f fixture) topUp(t *testing.T, minor int64, bankRef string) transaction.Transaction {
	t.Helper()
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Create(ctx, tx, transaction.New{
			Type: transaction.TypeTopUp, Status: transaction.StatusPending, InitiatorUserID: f.user,
			DestinationWalletID: f.wallet.ID, Amount: egp(t, minor), IdempotencyKey: uuid.NewString(),
		})
		if err != nil || bankRef == "" {
			return err
		}
		txn, err = f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing,
			transaction.Patch{BankReference: &bankRef})
		return err
	}))
	return txn
}

// fund credits the wallet with minor piasters through a completed top-up.
func (f fixture) fund(t *testing.T, minor int64) {
	t.Helper()
	ref := "FUND-" + uuid.NewString()
	txn := f.topUp(t, minor, ref)
	require.NoError(t, f.apply(t, txn.ID, succeeded(t, minor, ref)))
}

// pendingWithdrawal creates a funded, reserved PENDING withdrawal of minor piasters, as
// RequestWithdrawal does: the transaction with its bank account snapshot and the
// WITHDRAWAL_RESERVE entry (Dr user wallet / Cr WITHDRAWALS_IN_TRANSIT).
func (f fixture) pendingWithdrawal(t *testing.T, minor int64) transaction.Transaction {
	t.Helper()
	f.fund(t, minor)
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Create(ctx, tx, transaction.New{
			Type: transaction.TypeWithdrawal, Status: transaction.StatusPending, InitiatorUserID: f.user,
			SourceWalletID: f.wallet.ID, Amount: egp(t, minor), IdempotencyKey: uuid.NewString(),
			BankAccount: &transaction.BankAccountSnapshot{IBAN: "EG380019000500000000263180002", HolderName: "Test User"},
		})
		if err != nil {
			return err
		}
		inTransit, err := f.ledger.GetAccountByCode(ctx, tx, ledger.CodeWithdrawalsInTransit)
		if err != nil {
			return err
		}
		_, err = f.ledger.Post(ctx, tx, ledger.Entry{TransactionID: txn.ID, Type: ledger.EntryWithdrawalReserve,
			Postings: []ledger.Posting{
				{AccountID: f.wallet.LedgerAccountID, Direction: ledger.Debit, Amount: egp(t, minor)},
				{AccountID: inTransit.ID, Direction: ledger.Credit, Amount: egp(t, minor)},
			}})
		return err
	}))
	return txn
}

// withdrawal is a reserved withdrawal the bank accepted (PROCESSING with bankRef).
func (f fixture) withdrawal(t *testing.T, bankRef string) transaction.Transaction {
	t.Helper()
	txn := f.pendingWithdrawal(t, amount)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing,
			transaction.Patch{BankReference: &bankRef})
		return err
	}))
	return txn
}

func (f fixture) apply(t *testing.T, txnID uuid.UUID, o bank.Outcome) error {
	t.Helper()
	return f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.ApplyBankOutcome(ctx, tx, txnID, o)
	})
}

func (f fixture) handle(t *testing.T, ev webhook.BankEvent) error {
	t.Helper()
	return f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.HandleBankEvent(ctx, tx, ev)
	})
}

func (f fixture) get(t *testing.T, id uuid.UUID) transaction.Transaction {
	t.Helper()
	txn, err := f.txns.Get(context.Background(), f.db.Pool(), id)
	require.NoError(t, err)
	return txn
}

func (f fixture) walletBalance(t *testing.T) int64 {
	t.Helper()
	b, err := f.ledger.Balance(context.Background(), f.db.Pool(), f.wallet.LedgerAccountID)
	require.NoError(t, err)
	return b.Amount()
}

func (f fixture) settlementBalance(t *testing.T) int64 {
	t.Helper()
	acc, err := f.ledger.GetAccountByCode(context.Background(), f.db.Pool(), ledger.CodeBankSettlement)
	require.NoError(t, err)
	return acc.Balance.Amount()
}

func (f fixture) entries(t *testing.T, txnID uuid.UUID) []string {
	t.Helper()
	rows, err := f.db.Pool().Query(context.Background(), `SELECT entry_type FROM journal_entries WHERE transaction_id = $1 ORDER BY created_at`, txnID)
	require.NoError(t, err)
	types, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return types
}

func (f fixture) discrepancyList(t *testing.T) []discrepancy.Discrepancy {
	t.Helper()
	list, err := f.discrepancies.List(context.Background(), discrepancy.Filter{Status: discrepancy.StatusOpen})
	require.NoError(t, err)
	return list
}

func succeeded(t *testing.T, minor int64, ref string) bank.Outcome {
	return bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusSucceeded, BankReference: ref, Amount: egp(t, minor)}
}

func failed(t *testing.T, minor int64, ref string) bank.Outcome {
	return bank.Outcome{Kind: bank.KindDebit, Status: bank.StatusFailed, BankReference: ref, Amount: egp(t, minor),
		FailureCode: "INSUFFICIENT_FUNDS", FailureMessage: "The account has insufficient funds."}
}

// assertNoMoney checks that no ledger entry was posted for the transaction and no money moved.
func (f fixture) assertNoMoney(t *testing.T, txnID uuid.UUID) {
	t.Helper()
	assert.Empty(t, f.entries(t, txnID))
	assert.Zero(t, f.walletBalance(t))
	assert.Zero(t, f.settlementBalance(t))
}

func (f fixture) assertOneDiscrepancy(t *testing.T, kind discrepancy.Kind, txnID uuid.UUID) map[string]any {
	t.Helper()
	list := f.discrepancyList(t)
	require.Len(t, list, 1)
	assert.Equal(t, kind, list[0].Kind)
	assert.Equal(t, discrepancy.SeverityCritical, list[0].Severity)
	assert.Equal(t, txnID, list[0].TransactionID)
	var details map[string]any
	require.NoError(t, json.Unmarshal(list[0].Details, &details))
	return details
}

// AC-1.
func TestApplyBankOutcome_TopUp_Succeeded(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "BSIM-1")

	require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-1")))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusCompleted, got.Status)
	require.NotNil(t, got.CompletedAt)
	assert.Equal(t, int64(amount), f.walletBalance(t))
	assert.Equal(t, int64(amount), f.settlementBalance(t))
	assert.Equal(t, []string{string(ledger.EntryTopUpSettle)}, f.entries(t, txn.ID))
	assert.Empty(t, f.discrepancyList(t))
}

// AC-2.
func TestApplyBankOutcome_TopUp_Failed(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "BSIM-1")

	require.NoError(t, f.apply(t, txn.ID, failed(t, amount, "BSIM-1")))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusFailed, got.Status)
	assert.Equal(t, "INSUFFICIENT_FUNDS", got.FailureCode)
	assert.Equal(t, "The account has insufficient funds.", got.FailureMessage)
	assert.NotNil(t, got.CompletedAt)
	f.assertNoMoney(t, txn.ID)
	assert.Empty(t, f.discrepancyList(t))
}

// AC-3 (ApplyBankOutcome level) and AC-4.
func TestApplyBankOutcome_TopUp_Terminal(t *testing.T) {
	t.Run("completed: same outcome is a no-op, a contradiction is recorded once", func(t *testing.T) {
		f := setup(t)
		txn := f.topUp(t, amount, "BSIM-1")
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-1")))
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-1")))
		assert.Empty(t, f.discrepancyList(t))

		for range 2 {
			require.NoError(t, f.apply(t, txn.ID, failed(t, amount, "BSIM-1")))
		}
		details := f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, "COMPLETED", details["transaction_status"])
		assert.Equal(t, "FAILED", details["outcome_status"])
		assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
		assert.Equal(t, int64(amount), f.walletBalance(t), "credited once")
		assert.Len(t, f.entries(t, txn.ID), 1)
	})
	t.Run("failed: a later success is recorded, not credited", func(t *testing.T) {
		f := setup(t)
		txn := f.topUp(t, amount, "BSIM-1")
		require.NoError(t, f.apply(t, txn.ID, failed(t, amount, "BSIM-1")))
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-1")))

		f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, transaction.StatusFailed, f.get(t, txn.ID).Status)
		f.assertNoMoney(t, txn.ID)
	})
}

// AC-5.
func TestApplyBankOutcome_TopUp_AmountMismatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ref    string
		status transaction.Status
	}{{"processing", "BSIM-1", transaction.StatusProcessing}, {"pending", "", transaction.StatusPending}} {
		t.Run(tt.name, func(t *testing.T) {
			f := setup(t)
			txn := f.topUp(t, amount, tt.ref)
			require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount-1, "BSIM-1")))

			details := f.assertOneDiscrepancy(t, discrepancy.KindAmountMismatch, txn.ID)
			assert.EqualValues(t, amount, details["transaction_amount_minor"])
			assert.EqualValues(t, amount-1, details["outcome_amount_minor"])
			got := f.get(t, txn.ID)
			assert.Equal(t, tt.status, got.Status, "status unchanged")
			assert.Equal(t, tt.ref, got.BankReference)
			f.assertNoMoney(t, txn.ID)
		})
	}
}

// AC-6 at the ApplyBankOutcome level: PENDING → PROCESSING (reference stored) → final.
func TestApplyBankOutcome_TopUp_Pending(t *testing.T) {
	f := setup(t)
	ok := f.topUp(t, amount, "")
	require.NoError(t, f.apply(t, ok.ID, succeeded(t, amount, "BSIM-OK")))
	got := f.get(t, ok.ID)
	assert.Equal(t, transaction.StatusCompleted, got.Status)
	assert.Equal(t, "BSIM-OK", got.BankReference)

	ko := f.topUp(t, amount, "")
	require.NoError(t, f.apply(t, ko.ID, failed(t, amount, "BSIM-KO")))
	got = f.get(t, ko.ID)
	assert.Equal(t, transaction.StatusFailed, got.Status)
	assert.Equal(t, "BSIM-KO", got.BankReference)
	assert.Equal(t, int64(amount), f.walletBalance(t), "only the successful one")
}

// AC-7.
func TestApplyBankOutcome_TopUp_CreditsDespiteFreezeAndMaxBalance(t *testing.T) {
	f := setup(t)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'FROZEN' WHERE id = $1`, f.wallet.ID)
	require.NoError(t, err)
	const aboveMax = 15_000_000 // the default LIMIT_MAX_BALANCE is 10 000 000
	txn := f.topUp(t, aboveMax, "BSIM-1")

	require.NoError(t, f.apply(t, txn.ID, succeeded(t, aboveMax, "BSIM-1")))

	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(aboveMax), f.walletBalance(t))
}

// AC-9 and the other mismatches that are never applied.
func TestApplyBankOutcome_Mismatches(t *testing.T) {
	t.Run("debit outcome for a withdrawal", func(t *testing.T) {
		f := setup(t)
		txn := f.withdrawal(t, "BSIM-W")
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-W")))
		details := f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, "WITHDRAWAL", details["transaction_type"])
		assert.Equal(t, "DEBIT", details["outcome_kind"])
		assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	})
	t.Run("payout outcome for a top-up", func(t *testing.T) {
		f := setup(t)
		txn := f.topUp(t, amount, "BSIM-1")
		o := succeeded(t, amount, "BSIM-1")
		o.Kind = bank.KindPayout
		require.NoError(t, f.apply(t, txn.ID, o))
		f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		f.assertNoMoney(t, txn.ID)
	})
	t.Run("another bank reference", func(t *testing.T) {
		f := setup(t)
		txn := f.topUp(t, amount, "BSIM-1")
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-2")))
		details := f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, "BSIM-1", details["transaction_bank_reference"])
		assert.Equal(t, "BSIM-2", details["outcome_bank_reference"])
		assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
		f.assertNoMoney(t, txn.ID)
	})
}

func TestApplyBankOutcome_Errors(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "BSIM-1")
	pending := succeeded(t, amount, "BSIM-1")
	pending.Status = bank.StatusPending
	assert.ErrorIs(t, f.apply(t, txn.ID, pending), payment.ErrNotFinal)

	assert.ErrorIs(t, f.apply(t, uuid.New(), succeeded(t, amount, "BSIM-X")), transaction.ErrNotFound)
	assert.Empty(t, f.discrepancyList(t))
	assert.Empty(t, f.entries(t, txn.ID))
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
}

func event(t *testing.T, txn transaction.Transaction, eventType, ref string, minor int64) webhook.BankEvent {
	ev := webhook.BankEvent{
		EventID: uuid.NewString(), Type: eventType, BankReference: ref, IdempotencyKey: txn.ID,
		Amount: egp(t, minor), Status: webhook.StatusSucceeded, OccurredAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
	}
	if eventType == webhook.EventDebitFailed || eventType == webhook.EventPayoutFailed {
		ev.Status, ev.FailureCode, ev.FailureMessage = webhook.StatusFailed, "INSUFFICIENT_FUNDS", "The account has insufficient funds."
	}
	return ev
}

// AC-3: five deliveries of the same outcome (new event IDs, so the inbox cannot dedupe them).
func TestHandleBankEvent_RepeatedOutcomes(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "BSIM-1")
	for range 5 {
		require.NoError(t, f.handle(t, event(t, txn, webhook.EventDebitSucceeded, "BSIM-1", amount)))
	}
	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(amount), f.walletBalance(t))
	assert.Len(t, f.entries(t, txn.ID), 1)
	assert.Empty(t, f.discrepancyList(t))
}

// AC-3 under concurrency: the same outcome delivered by 5 workers at once, before the bank
// reference is stored (each finds the transaction by its key, then the row lock serializes them).
func TestHandleBankEvent_ConcurrentOutcomes(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "")
	errs := make([]error, 5)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = f.handle(t, event(t, txn, webhook.EventDebitSucceeded, "BSIM-1", amount)) })
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(amount), f.walletBalance(t))
	assert.Len(t, f.entries(t, txn.ID), 1)
	assert.Empty(t, f.discrepancyList(t))
}

// AC-6: the webhook arrives before the outbox worker stored the bank reference; the event is
// matched by its idempotency key (our transaction ID).
func TestHandleBankEvent_BeforeBankReference(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "")
	require.Equal(t, transaction.StatusPending, txn.Status)

	require.NoError(t, f.handle(t, event(t, txn, webhook.EventDebitSucceeded, "BSIM-EARLY", amount)))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusCompleted, got.Status)
	assert.Equal(t, "BSIM-EARLY", got.BankReference)
	assert.Equal(t, int64(amount), f.walletBalance(t))
}

func TestHandleBankEvent_Lookup(t *testing.T) {
	t.Run("no transaction → unknown, retried by the inbox", func(t *testing.T) {
		f := setup(t)
		ghost := transaction.Transaction{ID: uuid.New()}
		err := f.handle(t, event(t, ghost, webhook.EventDebitSucceeded, "BSIM-NONE", amount))
		assert.ErrorIs(t, err, webhook.ErrUnknownTransaction)
	})
	t.Run("payout events are applied (T019)", func(t *testing.T) {
		f := setup(t)
		w := f.withdrawal(t, "BSIM-W")
		require.NoError(t, f.handle(t, event(t, w, webhook.EventPayoutSucceeded, "BSIM-W", amount)))
		assert.Equal(t, transaction.StatusCompleted, f.get(t, w.ID).Status)
	})
	t.Run("reference of one transaction, idempotency key of another", func(t *testing.T) {
		f := setup(t)
		a := f.topUp(t, amount, "BSIM-A")
		b := f.topUp(t, amount, "")
		require.NoError(t, f.handle(t, event(t, b, webhook.EventDebitSucceeded, "BSIM-A", amount)))
		details := f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, a.ID)
		assert.Equal(t, b.ID.String(), details["event_idempotency_key"])
		assert.Equal(t, transaction.StatusProcessing, f.get(t, a.ID).Status)
		assert.Equal(t, transaction.StatusPending, f.get(t, b.ID).Status)
		assert.Zero(t, f.walletBalance(t))
	})
}

// The full path: a signed webhook through the inbox to payment (as wired in cmd/wallet).
func TestInbox_AppliesDebitThroughPayment(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, amount, "BSIM-1")
	secret := []byte("test-webhook-secret-of-32-bytes!")
	inbox, err := webhook.NewService(f.db.Pool(), f.db, f.discrepancies, f.clock,
		webhook.Config{Workers: 2, MaxAttempts: 3, PollInterval: 10 * time.Millisecond}, nil, nil)
	require.NoError(t, err)
	inbox.SetHandler(f.payment)
	mux := http.NewServeMux()
	webhook.NewHandler(inbox, secret, f.clock, nil, nil).Routes(mux)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- inbox.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	body, err := json.Marshal(banksim.Event{
		EventID: uuid.New(), EventType: webhook.EventDebitSucceeded, BankReference: "BSIM-1", IdempotencyKey: txn.ID,
		AmountMinor: amount, Currency: "EGP", Status: "SUCCEEDED", OccurredAt: f.clock.Now(),
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/bank", bytes.NewReader(body))
	req.Header.Set(webhook.SignatureHeader, banksim.Sign(secret, f.clock.Now(), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool { return f.get(t, txn.ID).Status == transaction.StatusCompleted }, 10*time.Second, 10*time.Millisecond)
	assert.Equal(t, int64(amount), f.walletBalance(t))
	var status string
	require.Eventually(t, func() bool {
		err := f.db.Pool().QueryRow(context.Background(), `SELECT status FROM inbox_events`).Scan(&status)
		return err == nil && status == "DONE"
	}, 10*time.Second, 10*time.Millisecond)
}

//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banktest"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

const testIBAN = "EG380019000500000000263180000"

// withBank rebuilds the payment service of f with gw as its bank.
func (f fixture) withBank(gw bank.Gateway) *payment.Service {
	return payment.NewService(payment.Deps{
		DB: f.db, Wallets: f.wallets, Transactions: f.txns, Ledger: f.ledger, Discrepancies: f.discrepancies,
		Bank: gw, Clock: f.clock,
	})
}

// pendingTopUp creates a PENDING top-up with a bank account snapshot, as RequestTopUp does.
func (f fixture) pendingTopUp(t *testing.T) transaction.Transaction {
	t.Helper()
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Create(ctx, tx, transaction.New{
			Type: transaction.TypeTopUp, Status: transaction.StatusPending, InitiatorUserID: f.user,
			DestinationWalletID: f.wallet.ID, Amount: egp(t, amount), IdempotencyKey: uuid.NewString(),
			BankAccount: &transaction.BankAccountSnapshot{IBAN: testIBAN, HolderName: "Test User"},
		})
		return err
	}))
	return txn
}

func deliver(t *testing.T, svc *payment.Service, txnID uuid.UUID, attempt int) queue.Result {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"transaction_id": txnID})
	require.NoError(t, err)
	h := svc.OutboxHandlers()[payment.EventTopUpRequested]
	require.NotNil(t, h)
	return h(context.Background(), queue.Job{ID: uuid.New(), Type: payment.EventTopUpRequested, Payload: payload, Attempts: attempt})
}

// AC-4: accepted → PROCESSING with the bank's reference; the event is done.
func TestTopUpHandler_Accept(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingTopUp(t)

	assert.Equal(t, queue.Done(), deliver(t, f.withBank(gw), txn.ID, 1))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, got.Status)
	assert.NotEmpty(t, got.BankReference)
	calls := gw.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "CreateDebit", calls[0].Op)
	assert.Equal(t, txn.ID, calls[0].Key, "the bank idempotency key is the transaction ID")
	assert.Equal(t, testIBAN, calls[0].Debit.IBAN)
	assert.Equal(t, "Test User", calls[0].Debit.HolderName)
	assert.Equal(t, txn.Amount, calls[0].Debit.Amount)
	f.assertNoMoney(t, txn.ID)
}

// AC-4: a business rejection fails the top-up at once with the bank's code (TU-BR-5).
func TestTopUpHandler_Reject(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingTopUp(t)
	gw.Script(txn.ID, banktest.Response{Err: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "The bank account is not valid."}})

	assert.Equal(t, queue.Done(), deliver(t, f.withBank(gw), txn.ID, 1))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusFailed, got.Status)
	assert.Equal(t, "INVALID_ACCOUNT", got.FailureCode)
	assert.Equal(t, "The bank account is not valid.", got.FailureMessage)
	assert.Len(t, gw.Calls(), 1)
	f.assertNoMoney(t, txn.ID)
}

// AC-5: temporary failures are retried with the same key until the bank accepts (TU-BR-5).
func TestTopUpHandler_TemporaryThenAccept(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingTopUp(t)
	gw.Script(txn.ID, banktest.Response{Err: bank.ErrTemporary}, banktest.Response{Err: bank.ErrTemporary}, banktest.Response{Err: bank.ErrTemporary})
	svc := f.withBank(gw)

	for attempt := 1; attempt <= 3; attempt++ {
		res := deliver(t, svc, txn.ID, attempt)
		assert.NotEqual(t, queue.Done(), res, "attempt %d is retried", attempt)
		assert.Equal(t, transaction.StatusPending, f.get(t, txn.ID).Status)
	}
	assert.Equal(t, queue.Done(), deliver(t, svc, txn.ID, 4))

	calls := gw.Calls()
	require.Len(t, calls, 4)
	for _, c := range calls {
		assert.Equal(t, txn.ID, c.Key)
	}
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
}

// racingBank simulates the webhook arriving while the bank call is in flight: the bank
// accepts, and the outcome is applied before the handler stores the reference.
type racingBank struct {
	*banktest.Fake
	webhook func(bank.Acceptance)
}

func (b racingBank) CreateDebit(ctx context.Context, req bank.DebitRequest) (bank.Acceptance, error) {
	acc, err := b.Fake.CreateDebit(ctx, req)
	b.webhook(acc)
	return acc, err
}

// AC-6: when the transaction already moved on, the handler changes nothing and is done.
func TestTopUpHandler_AlreadyProgressed(t *testing.T) {
	t.Run("before the call: no bank call", func(t *testing.T) {
		f := setup(t)
		gw := banktest.New()
		txn := f.pendingTopUp(t)
		require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, "BSIM-WEBHOOK")))

		assert.Equal(t, queue.Done(), deliver(t, f.withBank(gw), txn.ID, 1))
		assert.Empty(t, gw.Calls())
		assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
		assert.Equal(t, int64(amount), f.walletBalance(t), "credited once, by the webhook")
	})
	t.Run("during the call: the transition conflicts", func(t *testing.T) {
		f := setup(t)
		txn := f.pendingTopUp(t)
		gw := racingBank{Fake: banktest.New(), webhook: func(acc bank.Acceptance) {
			require.NoError(t, f.apply(t, txn.ID, succeeded(t, amount, acc.BankReference)))
		}}

		assert.Equal(t, queue.Done(), deliver(t, f.withBank(gw), txn.ID, 1))
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusCompleted, got.Status, "the webhook's result stands")
		assert.Equal(t, int64(amount), f.walletBalance(t))
		assert.Len(t, f.entries(t, txn.ID), 1)
	})
}

// AC-8: a wallet frozen after the top-up was accepted does not stop the bank call (TU-BR-8).
func TestTopUpHandler_FrozenAfterAcceptance(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingTopUp(t)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'FROZEN' WHERE id = $1`, f.wallet.ID)
	require.NoError(t, err)

	assert.Equal(t, queue.Done(), deliver(t, f.withBank(gw), txn.ID, 1))
	assert.Len(t, gw.Calls(), 1)
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
}

func TestTopUpHandler_BadEvents(t *testing.T) {
	f := setup(t)
	svc := f.withBank(banktest.New())
	h := svc.OutboxHandlers()[payment.EventTopUpRequested]
	assert.Equal(t, queue.Fail("transaction not found: "+uuid.Nil.String()), deliver(t, svc, uuid.Nil, 1))
	res := h(context.Background(), queue.Job{Payload: []byte(`{`), Attempts: 1})
	assert.NotEqual(t, queue.Done(), res)
}

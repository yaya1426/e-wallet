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
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

func deliverWithdrawal(t *testing.T, svc *payment.Service, txnID uuid.UUID, attempt int) queue.Result {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"transaction_id": txnID})
	require.NoError(t, err)
	h := svc.OutboxHandlers()[payment.EventWithdrawalRequested]
	require.NotNil(t, h)
	return h(context.Background(), queue.Job{ID: uuid.New(), Type: payment.EventWithdrawalRequested, Payload: payload, Attempts: attempt})
}

func (f fixture) inTransitBalance(t *testing.T) int64 {
	t.Helper()
	acc, err := f.ledger.GetAccountByCode(context.Background(), f.db.Pool(), ledger.CodeWithdrawalsInTransit)
	require.NoError(t, err)
	return acc.Balance.Amount()
}

// AC-6: accepted → PROCESSING with the bank's reference, the money stays reserved.
func TestWithdrawalHandler_Accept(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingWithdrawal(t, 20_000)

	assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 1))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, got.Status)
	assert.NotEmpty(t, got.BankReference)
	calls := gw.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "CreatePayout", calls[0].Op)
	assert.Equal(t, txn.ID, calls[0].Key)
	assert.Equal(t, "EG380019000500000000263180002", calls[0].Payout.IBAN)
	assert.Equal(t, txn.Amount, calls[0].Payout.Amount)
	assert.Zero(t, f.walletBalance(t), "still reserved")
	assert.Equal(t, int64(20_000), f.inTransitBalance(t))
	assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve)}, f.entries(t, txn.ID))
}

// AC-6: a synchronous rejection fails the withdrawal and refunds it in one transaction.
func TestWithdrawalHandler_RejectRefunds(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingWithdrawal(t, 20_000)
	gw.Script(txn.ID, banktest.Response{Err: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "The bank account is not valid."}})

	assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 1))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusFailed, got.Status)
	assert.Equal(t, "INVALID_ACCOUNT", got.FailureCode)
	assert.Equal(t, "The bank account is not valid.", got.FailureMessage)
	assert.Equal(t, int64(20_000), f.walletBalance(t), "balance restored")
	assert.Zero(t, f.inTransitBalance(t))
	assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, f.entries(t, txn.ID))

	// Delivered again (at-least-once): no second call, no second refund (WD-BR-6).
	assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 2))
	assert.Len(t, gw.Calls(), 1)
	assert.Len(t, f.entries(t, txn.ID), 2)
}

// AC-6: temporary failures are retried with the same key; paid once, never refunded (WD-BR-4/5).
func TestWithdrawalHandler_TemporaryThenAccept(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingWithdrawal(t, 20_000)
	gw.Script(txn.ID, banktest.Response{Err: bank.ErrTemporary}, banktest.Response{Err: bank.ErrTemporary}, banktest.Response{Err: bank.ErrTemporary})
	svc := f.withBank(gw)

	for attempt := 1; attempt <= 3; attempt++ {
		assert.NotEqual(t, queue.Done(), deliverWithdrawal(t, svc, txn.ID, attempt))
		assert.Equal(t, transaction.StatusPending, f.get(t, txn.ID).Status)
		assert.Equal(t, int64(20_000), f.inTransitBalance(t), "never refunded on a timeout")
	}
	assert.Equal(t, queue.Done(), deliverWithdrawal(t, svc, txn.ID, 4))
	calls := gw.Calls()
	require.Len(t, calls, 4)
	for _, c := range calls {
		assert.Equal(t, txn.ID, c.Key)
	}
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	assert.Len(t, f.entries(t, txn.ID), 1, "no refund")
}

// AC-7: the transaction already moved on → Done, nothing changed.
func TestWithdrawalHandler_AlreadyProgressed(t *testing.T) {
	t.Run("before the call", func(t *testing.T) {
		f := setup(t)
		gw := banktest.New()
		txn := f.pendingWithdrawal(t, 20_000)
		ref := "BSIM-OTHER"
		require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: &ref})
			return err
		}))
		assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 1))
		assert.Empty(t, gw.Calls())
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusProcessing, got.Status)
		assert.Equal(t, "BSIM-OTHER", got.BankReference)
	})
	t.Run("a rejection after it moved on: no refund", func(t *testing.T) {
		f := setup(t)
		txn := f.pendingWithdrawal(t, 20_000)
		gw := racingPayout{Fake: banktest.New(), during: func() {
			ref := "BSIM-RACE"
			require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
				_, err := f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: &ref})
				return err
			}))
		}}
		gw.Script(txn.ID, banktest.Response{Err: &bank.RejectedError{Code: "ACCOUNT_CLOSED", Message: "closed"}})

		assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 1))
		assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
		assert.Len(t, f.entries(t, txn.ID), 1, "no refund")
		assert.Equal(t, int64(20_000), f.inTransitBalance(t))
	})
}

// racingPayout runs during() while the payout call is in flight, then answers.
type racingPayout struct {
	*banktest.Fake
	during func()
}

func (b racingPayout) CreatePayout(ctx context.Context, req bank.PayoutRequest) (bank.Acceptance, error) {
	acc, err := b.Fake.CreatePayout(ctx, req)
	b.during()
	return acc, err
}

// WD-BR-8: the refund is credited even when the wallet is frozen.
func TestWithdrawalHandler_RefundToFrozenWallet(t *testing.T) {
	f := setup(t)
	gw := banktest.New()
	txn := f.pendingWithdrawal(t, 20_000)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'FROZEN' WHERE id = $1`, f.wallet.ID)
	require.NoError(t, err)
	gw.Script(txn.ID, banktest.Response{Err: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "invalid"}})

	assert.Equal(t, queue.Done(), deliverWithdrawal(t, f.withBank(gw), txn.ID, 1))
	assert.Equal(t, transaction.StatusFailed, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(20_000), f.walletBalance(t))
}

func TestWithdrawalHandler_WrongType(t *testing.T) {
	f := setup(t)
	txn := f.pendingTopUp(t)
	res := deliverWithdrawal(t, f.withBank(banktest.New()), txn.ID, 1)
	assert.NotEqual(t, queue.Done(), res)
	assert.Equal(t, transaction.StatusPending, f.get(t, txn.ID).Status)
}

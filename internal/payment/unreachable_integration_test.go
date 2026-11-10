//go:build integration

package payment_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

func (f fixture) failUnreachable(t *testing.T, txn transaction.Transaction) error {
	t.Helper()
	return f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.payment.FailUnreachable(ctx, tx, txn.ID)
	})
}

// T023: a request the bank never received fails with BANK_UNAVAILABLE; a withdrawal is refunded.
func TestFailUnreachable(t *testing.T) {
	t.Run("top-up", func(t *testing.T) {
		f := setup(t)
		txn := f.pendingTopUp(t)
		require.NoError(t, f.failUnreachable(t, txn))
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusFailed, got.Status)
		assert.Equal(t, payment.CodeBankUnavailable, got.FailureCode)
		assert.Equal(t, "The bank could not be reached. No money was moved.", got.FailureMessage)
		f.assertNoMoney(t, txn.ID)
	})
	t.Run("withdrawal", func(t *testing.T) {
		f := setup(t)
		txn := f.pendingWithdrawal(t, 20_000)
		require.NoError(t, f.failUnreachable(t, txn))
		got := f.get(t, txn.ID)
		assert.Equal(t, transaction.StatusFailed, got.Status)
		assert.Equal(t, payment.CodeBankUnavailable, got.FailureCode)
		assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, f.entries(t, txn.ID))
		assert.Equal(t, int64(20_000), f.walletBalance(t), "refunded")
		assert.Zero(t, f.inTransitBalance(t))
	})
	t.Run("not pending any more", func(t *testing.T) {
		f := setup(t)
		txn := f.processingWithdrawal(t, "BSIM-W")
		assert.ErrorIs(t, f.failUnreachable(t, txn), transaction.ErrStatusConflict)
		assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
		assert.Len(t, f.entries(t, txn.ID), 1, "no refund")
	})
}

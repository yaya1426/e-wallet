//go:build integration

package payment_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

const withdrawn = 20_000

func paid(t *testing.T, minor int64, ref string) bank.Outcome {
	return bank.Outcome{Kind: bank.KindPayout, Status: bank.StatusSucceeded, BankReference: ref, Amount: egp(t, minor)}
}

func payoutFailed(t *testing.T, minor int64, ref string) bank.Outcome {
	return bank.Outcome{Kind: bank.KindPayout, Status: bank.StatusFailed, BankReference: ref, Amount: egp(t, minor),
		FailureCode: "ACCOUNT_CLOSED", FailureMessage: "The account is closed."}
}

// processingWithdrawal reserves a withdrawal of `withdrawn` piasters and moves it to
// PROCESSING with ref, as the outbox worker does once the bank accepted the payout. The wallet
// is funded with exactly that amount, so its balance is 0 while the money is reserved.
func (f fixture) processingWithdrawal(t *testing.T, ref string) transaction.Transaction {
	t.Helper()
	txn := f.pendingWithdrawal(t, withdrawn)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: &ref})
		return err
	}))
	return txn
}

// AC-1.
func TestApplyBankOutcome_Withdrawal_Paid(t *testing.T) {
	f := setup(t)
	txn := f.processingWithdrawal(t, "BSIM-W")
	settlementBefore := f.settlementBalance(t) // the funding top-up brought in `withdrawn`

	require.NoError(t, f.apply(t, txn.ID, paid(t, withdrawn, "BSIM-W")))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusCompleted, got.Status)
	assert.NotNil(t, got.CompletedAt)
	assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalSettle)}, f.entries(t, txn.ID))
	assert.Zero(t, f.inTransitBalance(t), "in-transit −20 000")
	assert.Equal(t, settlementBefore-withdrawn, f.settlementBalance(t), "BANK_SETTLEMENT −20 000")
	assert.Zero(t, f.walletBalance(t), "the wallet does not change again")
	assert.Empty(t, f.discrepancyList(t))
}

// AC-2.
func TestApplyBankOutcome_Withdrawal_FailedRefunds(t *testing.T) {
	f := setup(t)
	txn := f.processingWithdrawal(t, "BSIM-W")

	require.NoError(t, f.apply(t, txn.ID, payoutFailed(t, withdrawn, "BSIM-W")))

	got := f.get(t, txn.ID)
	assert.Equal(t, transaction.StatusFailed, got.Status)
	assert.Equal(t, "ACCOUNT_CLOSED", got.FailureCode)
	assert.Equal(t, "The account is closed.", got.FailureMessage)
	assert.Equal(t, []string{string(ledger.EntryWithdrawalReserve), string(ledger.EntryWithdrawalRefund)}, f.entries(t, txn.ID))
	assert.Equal(t, int64(withdrawn), f.walletBalance(t), "wallet +20 000")
	assert.Zero(t, f.inTransitBalance(t))
}

// AC-3: a repeated outcome is a no-op; a contradiction is recorded once, without money change.
func TestApplyBankOutcome_Withdrawal_Terminal(t *testing.T) {
	t.Run("completed, then failed: possibly paid twice is not refunded", func(t *testing.T) {
		f := setup(t)
		txn := f.processingWithdrawal(t, "BSIM-W")
		for range 3 {
			require.NoError(t, f.apply(t, txn.ID, paid(t, withdrawn, "BSIM-W")))
		}
		require.Len(t, f.entries(t, txn.ID), 2, "settled once")
		for range 2 {
			require.NoError(t, f.apply(t, txn.ID, payoutFailed(t, withdrawn, "BSIM-W")))
		}
		details := f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, "COMPLETED", details["transaction_status"])
		assert.Equal(t, "FAILED", details["outcome_status"])
		assert.Equal(t, transaction.StatusCompleted, f.get(t, txn.ID).Status)
		assert.Zero(t, f.walletBalance(t), "no refund")
		assert.Len(t, f.entries(t, txn.ID), 2)
	})
	t.Run("failed, then paid: the user may have been paid twice", func(t *testing.T) {
		f := setup(t)
		txn := f.processingWithdrawal(t, "BSIM-W")
		require.NoError(t, f.apply(t, txn.ID, payoutFailed(t, withdrawn, "BSIM-W")))
		require.NoError(t, f.apply(t, txn.ID, payoutFailed(t, withdrawn, "BSIM-W")))
		require.Len(t, f.entries(t, txn.ID), 2, "refunded once")
		require.NoError(t, f.apply(t, txn.ID, paid(t, withdrawn, "BSIM-W")))

		f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
		assert.Equal(t, transaction.StatusFailed, f.get(t, txn.ID).Status)
		assert.Equal(t, int64(withdrawn), f.walletBalance(t), "the refund stays")
		assert.Len(t, f.entries(t, txn.ID), 2)
	})
}

// AC-4: amount mismatch → discrepancy, still PROCESSING, the money stays reserved.
func TestApplyBankOutcome_Withdrawal_AmountMismatch(t *testing.T) {
	f := setup(t)
	txn := f.processingWithdrawal(t, "BSIM-W")

	require.NoError(t, f.apply(t, txn.ID, paid(t, withdrawn+1, "BSIM-W")))

	f.assertOneDiscrepancy(t, discrepancy.KindAmountMismatch, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(withdrawn), f.inTransitBalance(t))
	assert.Zero(t, f.walletBalance(t))
	assert.Len(t, f.entries(t, txn.ID), 1)
}

// AC-5: the refund is credited even when the wallet is frozen.
func TestApplyBankOutcome_Withdrawal_RefundToFrozenWallet(t *testing.T) {
	f := setup(t)
	txn := f.processingWithdrawal(t, "BSIM-W")
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'FROZEN' WHERE id = $1`, f.wallet.ID)
	require.NoError(t, err)

	require.NoError(t, f.apply(t, txn.ID, payoutFailed(t, withdrawn, "BSIM-W")))
	assert.Equal(t, transaction.StatusFailed, f.get(t, txn.ID).Status)
	assert.Equal(t, int64(withdrawn), f.walletBalance(t))
}

// AC-6: the webhook beats the outbox worker; matched by idempotency key → PROCESSING → final.
func TestHandleBankEvent_PayoutBeforeBankReference(t *testing.T) {
	for _, tt := range []struct {
		eventType string
		status    transaction.Status
		wallet    int64
	}{
		{webhook.EventPayoutSucceeded, transaction.StatusCompleted, 0},
		{webhook.EventPayoutFailed, transaction.StatusFailed, withdrawn},
	} {
		t.Run(tt.eventType, func(t *testing.T) {
			f := setup(t)
			txn := f.pendingWithdrawal(t, withdrawn)
			require.NoError(t, f.handle(t, event(t, txn, tt.eventType, "BSIM-EARLY", withdrawn)))
			got := f.get(t, txn.ID)
			assert.Equal(t, tt.status, got.Status)
			assert.Equal(t, "BSIM-EARLY", got.BankReference)
			assert.Equal(t, tt.wallet, f.walletBalance(t))
			assert.Zero(t, f.inTransitBalance(t))
		})
	}
}

// A payout event for a top-up is a kind mismatch.
func TestHandleBankEvent_PayoutForTopUp(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, withdrawn, "BSIM-T")
	require.NoError(t, f.handle(t, event(t, txn, webhook.EventPayoutSucceeded, "BSIM-T", withdrawn)))
	f.assertOneDiscrepancy(t, discrepancy.KindStatusMismatch, txn.ID)
	assert.Equal(t, transaction.StatusProcessing, f.get(t, txn.ID).Status)
	f.assertNoMoney(t, txn.ID)
}

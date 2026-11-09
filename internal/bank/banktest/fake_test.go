package banktest_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banktest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

func TestFake_DefaultIsAHealthyIdempotentBank(t *testing.T) {
	f := banktest.New()
	ctx := context.Background()
	key := uuid.New()
	amount, err := money.NewPositive(500, money.EGP)
	require.NoError(t, err)

	_, err = f.GetOperation(ctx, key)
	require.ErrorIs(t, err, bank.ErrNotFound)

	first, err := f.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: key, Amount: amount})
	require.NoError(t, err)
	again, err := f.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: key, Amount: amount})
	require.NoError(t, err)
	assert.Equal(t, first, again, "same key → same acceptance")

	op, err := f.GetOperation(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, bank.Operation{IdempotencyKey: key, BankReference: first.BankReference, Kind: bank.KindDebit, Status: bank.StatusPending, Amount: amount}, op)

	other, err := f.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: uuid.New(), Amount: amount})
	require.NoError(t, err)
	assert.NotEqual(t, first.BankReference, other.BankReference)

	calls := f.Calls()
	require.Len(t, calls, 5)
	assert.Equal(t, "CreateDebit", calls[1].Op)
	require.NotNil(t, calls[1].Debit)
	assert.Equal(t, key, calls[1].Debit.IdempotencyKey)
	assert.Equal(t, "CreatePayout", calls[4].Op)
	assert.NotNil(t, calls[4].Payout)
}

func TestFake_ScriptedTimeoutsThenAccept(t *testing.T) {
	f := banktest.New()
	ctx := context.Background()
	key := uuid.New()
	f.Script(key,
		banktest.Response{Err: bank.ErrTemporary},
		banktest.Response{Err: bank.ErrTemporary},
		banktest.Response{Acceptance: bank.Acceptance{BankReference: "SCRIPTED"}},
	)

	for range 2 {
		_, err := f.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key})
		assert.ErrorIs(t, err, bank.ErrTemporary)
	}
	acc, err := f.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key})
	require.NoError(t, err)
	assert.Equal(t, "SCRIPTED", acc.BankReference)
	acc, err = f.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key})
	require.NoError(t, err)
	assert.Equal(t, "SCRIPTED", acc.BankReference, "after the script, the remembered operation answers")

	rejected := &bank.RejectedError{Code: "INVALID_ACCOUNT"}
	other := uuid.New()
	f.Script(other, banktest.Response{Err: rejected})
	_, err = f.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: other})
	assert.ErrorAs(t, err, &rejected)
	_, err = f.GetOperation(ctx, other)
	assert.ErrorIs(t, err, bank.ErrNotFound, "a rejected request creates no operation")
}

func TestFake_SetOperation(t *testing.T) {
	f := banktest.New()
	key := uuid.New()
	f.SetOperation(bank.Operation{IdempotencyKey: key, BankReference: "B", Kind: bank.KindPayout, Status: bank.StatusSucceeded})

	op, err := f.GetOperation(context.Background(), key)

	require.NoError(t, err)
	assert.Equal(t, bank.StatusSucceeded, op.Status)
}

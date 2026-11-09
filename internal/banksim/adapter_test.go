package banksim_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	bankclient "github.com/yaya1426/e-wallet/internal/bank/banksim"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// TestBanksim_WithWalletAdapter checks that the wallet's adapter (T013) and the simulator
// agree on the contract (api/openapi/banksim.yaml), end to end over HTTP.
func TestBanksim_WithWalletAdapter(t *testing.T) {
	w, hook := newWallet(t)
	s := newSim(t, hook)
	client, err := bankclient.New(bankclient.Config{BaseURL: s.url, Timeout: time.Second}, nil, nil)
	require.NoError(t, err)
	ctx := context.Background()
	amount, err := money.NewPositive(25_000, money.EGP)
	require.NoError(t, err)

	key := uuid.New()
	acc, err := client.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key, Amount: amount, IBAN: "EG380019000500000000263180001", HolderName: "Mona"})
	require.NoError(t, err)
	again, err := client.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key, Amount: amount, IBAN: "EG380019000500000000263180001", HolderName: "Mona"})
	require.NoError(t, err)
	assert.Equal(t, acc, again, "a retried call gets the same acceptance")

	w.next(t)
	op, err := client.GetOperation(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, bank.Operation{
		IdempotencyKey: key, BankReference: acc.BankReference, Kind: bank.KindPayout, Status: bank.StatusFailed,
		Amount: amount, FailureCode: "ACCOUNT_CLOSED", FailureMessage: "The destination account is closed.",
	}, op)

	_, err = client.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: amount, IBAN: "EG380019000500000000263180002", HolderName: "Mona"})
	var rejected *bank.RejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "INVALID_ACCOUNT", rejected.Code)

	_, err = client.GetOperation(ctx, uuid.New())
	assert.ErrorIs(t, err, bank.ErrNotFound)
}

//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// createTxn inserts a transaction through the transaction service, optionally moving it
// along a status path.
func (f apiFixture) createTxn(t *testing.T, n transaction.New, path ...transaction.Status) transaction.Transaction {
	t.Helper()
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if txn, err = f.txns.Create(ctx, tx, n); err != nil {
			return err
		}
		from := txn.Status
		for _, to := range path {
			patch := transaction.Patch{}
			if to == transaction.StatusFailed {
				code, msg := "INSUFFICIENT_FUNDS", "The bank declined the debit."
				patch = transaction.Patch{FailureCode: &code, FailureMessage: &msg}
			}
			if txn, err = f.txns.Transition(ctx, tx, txn.ID, from, to, patch); err != nil {
				return err
			}
			from = to
		}
		return nil
	}))
	return txn
}

func egpAmount(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(amount, money.EGP)
	require.NoError(t, err)
	return m
}

func TestListMyTransactions_DirectionsAndCounterparty(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	f := newAPIFixtureWith(t, clk, nil)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 50_000) // a completed top-up (IN)
	f.fund(t, b.userID, b.walletID, 50_000)

	clk.Advance(time.Minute)
	failed := f.createTxn(t, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending,
		InitiatorUserID: uuid.MustParse(a.userID), DestinationWalletID: uuid.MustParse(a.walletID),
		Amount: egpAmount(t, 700), IdempotencyKey: "failed-topup"}, transaction.StatusFailed)
	clk.Advance(time.Minute)
	withdrawal := f.createTxn(t, transaction.New{Type: transaction.TypeWithdrawal, Status: transaction.StatusPending,
		InitiatorUserID: uuid.MustParse(a.userID), SourceWalletID: uuid.MustParse(a.walletID),
		Amount: egpAmount(t, 1_000), IdempotencyKey: "w-1"})
	clk.Advance(time.Minute)
	require.Equal(t, http.StatusCreated, f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 2_000, "lunch")).status)
	clk.Advance(time.Minute)
	require.Equal(t, http.StatusCreated, f.transfer(t, b.token, uuid.NewString(), transferBody(a.phone, 300, "")).status)

	r := f.call(t, http.MethodGet, "/v1/me/transactions", a.token, "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	items := r.body["items"].([]any)
	require.Len(t, items, 5)
	assert.Nil(t, r.body["next_cursor"])

	type row struct{ typ, status, dir string }
	got := make([]row, len(items))
	for i, it := range items {
		m := it.(map[string]any)
		got[i] = row{m["type"].(string), m["status"].(string), m["direction"].(string)}
	}
	assert.Equal(t, []row{
		{"P2P", "COMPLETED", "IN"},       // received from B (newest)
		{"P2P", "COMPLETED", "OUT"},      // sent to B
		{"WITHDRAWAL", "PENDING", "OUT"}, // withdrawal
		{"TOPUP", "FAILED", "IN"},        // failed top-up
		{"TOPUP", "COMPLETED", "IN"},     // funding top-up (oldest)
	}, got, "newest first, each with the caller's direction")

	received, sent := items[0].(map[string]any), items[1].(map[string]any)
	assert.Equal(t, map[string]any{"phone": b.phone, "masked_name": "Ahmed M. A."}, received["counterparty"])
	assert.Equal(t, map[string]any{"phone": b.phone, "masked_name": "Ahmed M. A."}, sent["counterparty"])
	assert.Equal(t, "lunch", sent["note"])
	assert.Nil(t, items[2].(map[string]any)["counterparty"], "only P2P rows have a counterparty")
	assert.Equal(t, withdrawal.ID.String(), items[2].(map[string]any)["id"])

	failedItem := items[3].(map[string]any)
	assert.Equal(t, failed.ID.String(), failedItem["id"])
	assert.Equal(t, "INSUFFICIENT_FUNDS", failedItem["failure_code"])
	assert.Equal(t, "The bank declined the debit.", failedItem["failure_message"])
	assert.NotNil(t, failedItem["completed_at"])

	bView := f.call(t, http.MethodGet, "/v1/me/transactions", b.token, "")
	require.Equal(t, http.StatusOK, bView.status)
	bItems := bView.body["items"].([]any)
	assert.Equal(t, "OUT", bItems[0].(map[string]any)["direction"], "the same P2P record is OUT for its sender")
	assert.Equal(t, map[string]any{"phone": a.phone, "masked_name": "Ahmed M. A."}, bItems[0].(map[string]any)["counterparty"])
}

func TestListMyTransactions_Pagination(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)) // frozen: equal created_at values
	f := newAPIFixtureWith(t, clk, nil)
	a := f.newParty(t, "+201001234567")
	want := map[string]bool{}
	for i := range 45 {
		txn := f.createTxn(t, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending,
			InitiatorUserID: uuid.MustParse(a.userID), DestinationWalletID: uuid.MustParse(a.walletID),
			Amount: egpAmount(t, int64(1000+i)), IdempotencyKey: uuid.NewString()})
		want[txn.ID.String()] = true
	}

	seen := map[string]bool{}
	var sizes []int
	cursor := ""
	for page := 0; page < 5; page++ {
		path := "/v1/me/transactions?limit=20"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := f.call(t, http.MethodGet, path, a.token, "")
		require.Equal(t, http.StatusOK, r.status, r.body)
		items := r.body["items"].([]any)
		sizes = append(sizes, len(items))
		for _, it := range items {
			id := it.(map[string]any)["id"].(string)
			assert.False(t, seen[id], "no item repeated: %s", id)
			seen[id] = true
		}
		if r.body["next_cursor"] == nil {
			break
		}
		cursor = r.body["next_cursor"].(string)
	}
	assert.Equal(t, []int{20, 20, 5}, sizes)
	assert.Equal(t, want, seen, "no item skipped")
}

func TestListMyTransactions_BadParameters(t *testing.T) {
	f := newAPIFixture(t)
	a := f.newParty(t, "+201001234567")
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=abc"} {
		r := f.call(t, http.MethodGet, "/v1/me/transactions"+q, a.token, "")
		assert.Equal(t, http.StatusBadRequest, r.status, q)
		assert.Equal(t, "INVALID_LIMIT", errorCode(r), q)
	}
	r := f.call(t, http.MethodGet, "/v1/me/transactions?cursor=garbage!!", a.token, "")
	assert.Equal(t, http.StatusBadRequest, r.status)
	assert.Equal(t, "INVALID_CURSOR", errorCode(r))
	assert.Equal(t, http.StatusUnauthorized, f.call(t, http.MethodGet, "/v1/me/transactions", "", "").status)
}

func TestGetTransaction_Visibility(t *testing.T) {
	f := newAPIFixture(t)
	a, b, c := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568"), f.newParty(t, "+201001234569")
	f.fund(t, a.userID, a.walletID, 10_000)
	sent := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 1_000, ""))
	require.Equal(t, http.StatusCreated, sent.status)
	id := sent.body["transaction_id"].(string)

	for _, p := range []party{a, b} {
		r := f.call(t, http.MethodGet, "/v1/transactions/"+id, p.token, "")
		assert.Equal(t, http.StatusOK, r.status, "both sides of the P2P")
		assert.Equal(t, id, r.body["id"])
	}
	assert.Equal(t, "OUT", f.call(t, http.MethodGet, "/v1/transactions/"+id, a.token, "").body["direction"])
	assert.Equal(t, "IN", f.call(t, http.MethodGet, "/v1/transactions/"+id, b.token, "").body["direction"])

	for name, path := range map[string]string{
		"another user's": "/v1/transactions/" + id,
		"unknown":        "/v1/transactions/" + uuid.NewString(),
		"not a UUID":     "/v1/transactions/not-a-uuid",
	} {
		r := f.call(t, http.MethodGet, path, c.token, "")
		assert.Equal(t, http.StatusNotFound, r.status, name)
		assert.Equal(t, "TRANSACTION_NOT_FOUND", errorCode(r), name)
	}
}

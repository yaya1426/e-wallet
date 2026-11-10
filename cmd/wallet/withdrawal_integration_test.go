//go:build integration

package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banktest"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/payment/paymenttest"
)

// newWithdrawalFixture is the API fixture with the in-transit invariant checked at the end
// (T018 AC-8; other suites create raw withdrawals without a reserve).
func newWithdrawalFixture(t *testing.T) apiFixture {
	t.Helper()
	f := newAPIFixture(t)
	t.Cleanup(func() { paymenttest.AssertInTransit(t, f.db.Pool()) })
	return f
}

func (f apiFixture) withdraw(t *testing.T, token, key string, amount int64) transferResult {
	t.Helper()
	return f.post(t, "/v1/withdrawals", token, key, topUpBody(amount))
}

func (f apiFixture) inTransit(t *testing.T) int64 {
	t.Helper()
	acc, err := f.ledger.GetAccountByCode(context.Background(), f.db.Pool(), ledger.CodeWithdrawalsInTransit)
	require.NoError(t, err)
	return acc.Balance.Amount()
}

func (f apiFixture) withdrawalsInFlight(t *testing.T) int {
	return f.countWhere(t, `SELECT count(*) FROM transactions WHERE type = 'WITHDRAWAL'`)
}

// AC-1.
func TestWithdrawal_Accepted(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 50_000)

	r := f.withdraw(t, p.token, uuid.NewString(), 20_000)

	require.Equal(t, http.StatusAccepted, r.status, r.body)
	assert.Equal(t, "WITHDRAWAL", r.body["type"])
	assert.Equal(t, "PENDING", r.body["status"])
	assert.EqualValues(t, 20_000, r.body["amount_minor"])
	txnID := r.body["transaction_id"].(string)

	assert.Equal(t, int64(30_000), f.balance(t, p.token), "reserved at once (WD-1)")
	assert.Equal(t, int64(20_000), f.inTransit(t))
	var src, iban string
	var dst *string
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT source_wallet_id::text, destination_wallet_id::text,
		bank_account_snapshot->>'iban' FROM transactions WHERE id = $1`, txnID).Scan(&src, &dst, &iban))
	assert.Equal(t, p.walletID, src)
	assert.Nil(t, dst)
	assert.Equal(t, testIBAN, iban)
	assert.Equal(t, 1, f.countWhere(t, `SELECT count(*) FROM journal_entries WHERE transaction_id = $1 AND entry_type = 'WITHDRAWAL_RESERVE'`, txnID))
	assert.Equal(t, 1, f.countWhere(t, `SELECT count(*) FROM outbox_events WHERE event_type = $1 AND aggregate_id = $2`,
		payment.EventWithdrawalRequested, txnID))
}

// AC-2: every check of the table, in order; nothing is written.
func TestWithdrawal_Errors(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 50_000)
	noBank := f.newParty(t, "+201001234568")
	f.fund(t, noBank.userID, noBank.walletID, 50_000)
	frozen := f.topUpParty(t, "+201001234569")
	f.fund(t, frozen.userID, frozen.walletID, 50_000)
	f.freeze(t, frozen.walletID)
	txnsBefore, eventsBefore, entriesBefore := f.count(t, "transactions"), f.outboxEvents(t), f.count(t, "journal_entries")

	tests := []struct {
		name, token, key, body string
		status                 int
		code                   string
	}{
		{"no token", "", uuid.NewString(), topUpBody(20_000), 401, "UNAUTHENTICATED"},
		{"malformed body", p.token, uuid.NewString(), `{"amount_minor":`, 400, "MALFORMED_REQUEST"},
		{"no key", p.token, "", topUpBody(20_000), 400, "IDEMPOTENCY_KEY_REQUIRED"},
		{"bad key", p.token, "nope", topUpBody(20_000), 400, "IDEMPOTENCY_KEY_INVALID"},
		{"zero amount", p.token, uuid.NewString(), topUpBody(0), 400, "INVALID_AMOUNT"},
		{"other currency", p.token, uuid.NewString(), `{"amount_minor":20000,"currency":"USD"}`, 400, "UNSUPPORTED_CURRENCY"},
		{"frozen", frozen.token, uuid.NewString(), topUpBody(20_000), 403, "WALLET_FROZEN"},
		{"no bank account", noBank.token, uuid.NewString(), topUpBody(20_000), 422, "BANK_ACCOUNT_REQUIRED"},
		{"below min", p.token, uuid.NewString(), topUpBody(999), 422, "AMOUNT_OUT_OF_RANGE"},
		{"above max", p.token, uuid.NewString(), topUpBody(3_000_001), 422, "AMOUNT_OUT_OF_RANGE"},
		{"range beats funds", p.token, uuid.NewString(), topUpBody(999), 422, "AMOUNT_OUT_OF_RANGE"},
		{"insufficient funds", p.token, uuid.NewString(), topUpBody(50_001), 422, "INSUFFICIENT_FUNDS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.post(t, "/v1/withdrawals", tt.token, tt.key, tt.body)
			assert.Equal(t, tt.status, r.status, r.body)
			assert.Equal(t, tt.code, errorCode(httpResult{body: r.body}))
		})
	}
	assert.Equal(t, txnsBefore, f.count(t, "transactions"))
	assert.Equal(t, eventsBefore, f.outboxEvents(t))
	assert.Equal(t, entriesBefore, f.count(t, "journal_entries"))
	assert.Equal(t, int64(50_000), f.balance(t, p.token))

	// The whole balance can be withdrawn.
	assert.Equal(t, http.StatusAccepted, f.withdraw(t, p.token, uuid.NewString(), 50_000).status)
	assert.Zero(t, f.balance(t, p.token))
}

// AC-3: 10 parallel withdrawals of 10 000 on a 50 000 balance → exactly 5 accepted.
func TestWithdrawal_ParallelNeverNegative(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 50_000)

	results := make([]transferResult, 10)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.withdraw(t, p.token, uuid.NewString(), 10_000) })
	}
	wg.Wait()

	accepted, refused := 0, 0
	for _, r := range results {
		switch r.status {
		case http.StatusAccepted:
			accepted++
		case http.StatusUnprocessableEntity:
			assert.Equal(t, "INSUFFICIENT_FUNDS", errorCode(httpResult{body: r.body}))
			refused++
		default:
			t.Errorf("unexpected answer %d %v", r.status, r.body)
		}
	}
	assert.Equal(t, 5, accepted)
	assert.Equal(t, 5, refused)
	assert.Zero(t, f.balance(t, p.token))
	assert.Equal(t, int64(50_000), f.inTransit(t))
	assert.Equal(t, 5, f.withdrawalsInFlight(t))
}

// AC-4: 5 000 000 sent today + 1 100 000 > 6 000 000; a FAILED withdrawal does not count.
func TestWithdrawal_DailyLimit(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	friend := f.newParty(t, "+201001234568")
	f.fund(t, p.userID, p.walletID, 9_000_000)
	for range 2 {
		require.Equal(t, http.StatusCreated, f.transfer(t, p.token, uuid.NewString(), transferBody(friend.phone, 2_000_000, "")).status)
	}
	// 4 000 000 by P2P, 1 000 000 by an accepted withdrawal = 5 000 000 today.
	require.Equal(t, http.StatusAccepted, f.withdraw(t, p.token, uuid.NewString(), 1_000_000).status)

	// A withdrawal the bank refuses is FAILED (and refunded): it does not count.
	failed := f.withdraw(t, p.token, uuid.NewString(), 500_000)
	require.Equal(t, http.StatusAccepted, failed.status)
	failedID := uuid.MustParse(failed.body["transaction_id"].(string))
	f.bank.Script(failedID, banktest.Response{Err: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "invalid"}})
	f.runOutbox(t)
	require.Eventually(t, func() bool { s, _ := f.txnStatus(t, failedID.String()); return s == "FAILED" }, 10*time.Second, 10*time.Millisecond)

	r := f.withdraw(t, p.token, uuid.NewString(), 1_100_000)
	assert.Equal(t, http.StatusUnprocessableEntity, r.status)
	assert.Equal(t, "DAILY_LIMIT_EXCEEDED", errorCode(httpResult{body: r.body}))
	assert.Equal(t, http.StatusAccepted, f.withdraw(t, p.token, uuid.NewString(), 1_000_000).status, "exactly the limit")
	require.Eventually(t, func() bool {
		return f.countWhere(t, `SELECT count(*) FROM outbox_events WHERE status <> 'DONE'`) == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// AC-5: the same key twice → one withdrawal and one reserve; the second gets the replay.
func TestWithdrawal_Idempotent(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 50_000)
	key := uuid.NewString()

	first := f.withdraw(t, p.token, key, 20_000)
	second := f.withdraw(t, p.token, key, 20_000)
	require.Equal(t, http.StatusAccepted, first.status)
	assert.Equal(t, first.body, second.body)
	assert.True(t, second.replayed)
	assert.Equal(t, 1, f.withdrawalsInFlight(t))
	assert.Equal(t, int64(30_000), f.balance(t, p.token), "reserved once")

	// A stored INSUFFICIENT_FUNDS is replayed even after money arrives.
	poorKey := uuid.NewString()
	assert.Equal(t, http.StatusUnprocessableEntity, f.withdraw(t, p.token, poorKey, 40_000).status)
	f.fund(t, p.userID, p.walletID, 50_000)
	replay := f.withdraw(t, p.token, poorKey, 40_000)
	assert.Equal(t, http.StatusUnprocessableEntity, replay.status)
	assert.True(t, replay.replayed)
}

// AC-6 through the real wiring: the outbox pays out through the fake bank.
func TestWithdrawal_DeliveredToTheBank(t *testing.T) {
	f := newWithdrawalFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 50_000)
	f.runOutbox(t)

	r := f.withdraw(t, p.token, uuid.NewString(), 20_000)
	require.Equal(t, http.StatusAccepted, r.status)
	txnID := r.body["transaction_id"].(string)

	require.Eventually(t, func() bool { s, _ := f.txnStatus(t, txnID); return s == "PROCESSING" }, 10*time.Second, 10*time.Millisecond)
	calls := f.bank.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "CreatePayout", calls[0].Op)
	assert.Equal(t, txnID, calls[0].Key.String())
	assert.Equal(t, int64(30_000), f.balance(t, p.token))
}

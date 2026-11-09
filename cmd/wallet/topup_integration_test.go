//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/payment"
)

const testIBAN = "EG350003000100000000123450002"

// post sends a money POST with the given key ("" means no header) and token ("" means none).
func (f apiFixture) post(t *testing.T, path, token, key, body string) transferResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	return transferResult{status: resp.StatusCode, body: decoded, replayed: resp.Header.Get("Idempotent-Replayed") == "true"}
}

func topUpBody(amount int64) string {
	return fmt.Sprintf(`{"amount_minor":%d,"currency":"EGP"}`, amount)
}

func (f apiFixture) linkBank(t *testing.T, token string) {
	t.Helper()
	r := f.call(t, http.MethodPut, "/v1/me/bank-account", token, `{"iban":"`+testIBAN+`","holder_name":"Ahmed Ali"}`)
	require.Equal(t, http.StatusOK, r.status, r.body)
}

// topUpParty is a registered user with a linked bank account.
func (f apiFixture) topUpParty(t *testing.T, phone string) party {
	t.Helper()
	p := f.newParty(t, phone)
	f.linkBank(t, p.token)
	return p
}

func (f apiFixture) countWhere(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func (f apiFixture) pendingTopUps(t *testing.T) int {
	return f.countWhere(t, `SELECT count(*) FROM transactions WHERE type = 'TOPUP' AND status IN ('PENDING', 'PROCESSING')`)
}

func (f apiFixture) outboxEvents(t *testing.T) int {
	return f.countWhere(t, `SELECT count(*) FROM outbox_events`)
}

func (f apiFixture) txnStatus(t *testing.T, id string) (status, bankRef string) {
	t.Helper()
	var ref *string
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT status, bank_reference FROM transactions WHERE id = $1`, id).Scan(&status, &ref))
	if ref != nil {
		bankRef = *ref
	}
	return status, bankRef
}

// runOutbox delivers outbox events (the bank calls) until the test ends.
func (f apiFixture) runOutbox(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.api.outbox.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
}

// AC-1.
func TestTopUp_Accepted(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	entriesBefore := f.count(t, "journal_entries")

	r := f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(50_000))

	require.Equal(t, http.StatusAccepted, r.status, r.body)
	assert.False(t, r.replayed)
	assert.Equal(t, "TOPUP", r.body["type"])
	assert.Equal(t, "PENDING", r.body["status"])
	assert.EqualValues(t, 50_000, r.body["amount_minor"])
	assert.Equal(t, "EGP", r.body["currency"])
	_, err := time.Parse(time.RFC3339Nano, r.body["created_at"].(string))
	require.NoError(t, err)
	txnID := r.body["transaction_id"].(string)

	var typ, status, dst, iban, holder string
	var src *string
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT type, status, source_wallet_id::text,
		destination_wallet_id::text, bank_account_snapshot->>'iban', bank_account_snapshot->>'holder_name'
		FROM transactions WHERE id = $1`, txnID).Scan(&typ, &status, &src, &dst, &iban, &holder))
	assert.Equal(t, []string{"TOPUP", "PENDING", p.walletID, testIBAN, "Ahmed Ali"}, []string{typ, status, dst, iban, holder})
	assert.Nil(t, src)
	assert.Equal(t, 1, f.countWhere(t, `SELECT count(*) FROM outbox_events WHERE event_type = $1 AND aggregate_id = $2
		AND payload->>'transaction_id' = $2::text`, payment.EventTopUpRequested, txnID))
	assert.Zero(t, f.balance(t, p.token), "no money until the bank confirms (TU-BR-1)")
	assert.Equal(t, entriesBefore, f.count(t, "journal_entries"))
}

// AC-2: every check of the table, in order; nothing is created.
func TestTopUp_Errors(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	noBank := f.newParty(t, "+201001234568")
	frozen := f.topUpParty(t, "+201001234569")
	f.freeze(t, frozen.walletID)
	rich := f.topUpParty(t, "+201001234570")
	f.fund(t, rich.userID, rich.walletID, 9_950_000)
	txnsBefore, eventsBefore := f.count(t, "transactions"), f.outboxEvents(t)

	tests := []struct {
		name, token, key, body string
		status                 int
		code                   string
	}{
		{"no token", "", uuid.NewString(), topUpBody(50_000), 401, "UNAUTHENTICATED"},
		{"malformed body", p.token, uuid.NewString(), `{"amount_minor":`, 400, "MALFORMED_REQUEST"},
		{"unknown field", p.token, uuid.NewString(), `{"amount_minor":100,"currency":"EGP","x":1}`, 400, "MALFORMED_REQUEST"},
		{"no key", p.token, "", topUpBody(50_000), 400, "IDEMPOTENCY_KEY_REQUIRED"},
		{"bad key", p.token, "not-a-uuid", topUpBody(50_000), 400, "IDEMPOTENCY_KEY_INVALID"},
		{"zero amount", p.token, uuid.NewString(), topUpBody(0), 400, "INVALID_AMOUNT"},
		{"negative amount", p.token, uuid.NewString(), topUpBody(-5), 400, "INVALID_AMOUNT"},
		{"other currency", p.token, uuid.NewString(), `{"amount_minor":50000,"currency":"USD"}`, 400, "UNSUPPORTED_CURRENCY"},
		{"frozen", frozen.token, uuid.NewString(), topUpBody(50_000), 403, "WALLET_FROZEN"},
		{"frozen beats no bank account and range", frozen.token, uuid.NewString(), topUpBody(1), 403, "WALLET_FROZEN"},
		{"no bank account", noBank.token, uuid.NewString(), topUpBody(50_000), 422, "BANK_ACCOUNT_REQUIRED"},
		{"no bank account beats range", noBank.token, uuid.NewString(), topUpBody(1), 422, "BANK_ACCOUNT_REQUIRED"},
		{"below min", p.token, uuid.NewString(), topUpBody(999), 422, "AMOUNT_OUT_OF_RANGE"},
		{"above max", p.token, uuid.NewString(), topUpBody(3_000_001), 422, "AMOUNT_OUT_OF_RANGE"},
		{"max balance", rich.token, uuid.NewString(), topUpBody(50_001), 422, "WALLET_LIMIT_EXCEEDED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.post(t, "/v1/topups", tt.token, tt.key, tt.body)
			assert.Equal(t, tt.status, r.status, r.body)
			assert.Equal(t, tt.code, errorCode(httpResult{status: r.status, body: r.body}))
		})
	}
	assert.Equal(t, txnsBefore, f.count(t, "transactions"), "no transaction")
	assert.Equal(t, eventsBefore, f.outboxEvents(t), "no outbox event")

	// Boundaries are accepted: the min, the max, and exactly the maximum balance.
	for _, tt := range []struct {
		p      party
		amount int64
	}{{p, 1_000}, {p, 3_000_000}, {rich, 50_000}} {
		r := f.post(t, "/v1/topups", tt.p.token, uuid.NewString(), topUpBody(tt.amount))
		assert.Equal(t, http.StatusAccepted, r.status, r.body)
	}
}

// AC-3 (sequential) and stored business errors.
func TestTopUp_Idempotent(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	key := uuid.NewString()

	first := f.post(t, "/v1/topups", p.token, key, topUpBody(50_000))
	second := f.post(t, "/v1/topups", p.token, key, topUpBody(50_000))
	require.Equal(t, http.StatusAccepted, first.status)
	assert.Equal(t, first.status, second.status)
	assert.Equal(t, first.body, second.body)
	assert.True(t, second.replayed)
	assert.Equal(t, 1, f.pendingTopUps(t))
	assert.Equal(t, 1, f.outboxEvents(t))

	other := f.post(t, "/v1/topups", p.token, key, topUpBody(60_000))
	assert.Equal(t, http.StatusUnprocessableEntity, other.status)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", errorCode(httpResult{body: other.body}))

	// A business error is replayed too, even after the cause is gone.
	f.freeze(t, p.walletID)
	frozenKey := uuid.NewString()
	assert.Equal(t, http.StatusForbidden, f.post(t, "/v1/topups", p.token, frozenKey, topUpBody(50_000)).status)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'ACTIVE' WHERE id = $1`, p.walletID)
	require.NoError(t, err)
	replay := f.post(t, "/v1/topups", p.token, frozenKey, topUpBody(50_000))
	assert.Equal(t, http.StatusForbidden, replay.status)
	assert.True(t, replay.replayed)
	assert.Equal(t, 1, f.pendingTopUps(t))
}

// AC-3 (parallel): one transaction and one bank call; the other request gets 409 or the replay.
func TestTopUp_ParallelSameKey(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	key := uuid.NewString()
	results := make([]transferResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.post(t, "/v1/topups", p.token, key, topUpBody(50_000)) })
	}
	wg.Wait()

	accepted := 0
	for _, r := range results {
		switch r.status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			assert.Equal(t, "REQUEST_IN_PROGRESS", errorCode(httpResult{body: r.body}))
		default:
			t.Fatalf("unexpected answer %d %v", r.status, r.body)
		}
	}
	assert.GreaterOrEqual(t, accepted, 1)
	assert.Equal(t, 1, f.pendingTopUps(t))
	assert.Equal(t, 1, f.outboxEvents(t))

	f.runOutbox(t)
	require.Eventually(t, func() bool { return len(f.bank.Calls()) == 1 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.Len(t, f.bank.Calls(), 1, "one bank call")
}

// AC-7: balance 9 000 000 + pending 900 000 + 200 000 > 10 000 000.
func TestTopUp_MaxBalanceWithPending(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 9_000_000)
	require.Equal(t, http.StatusAccepted, f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(900_000)).status)

	r := f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(200_000))
	assert.Equal(t, http.StatusUnprocessableEntity, r.status)
	assert.Equal(t, "WALLET_LIMIT_EXCEEDED", errorCode(httpResult{body: r.body}))
	assert.Equal(t, http.StatusAccepted, f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(100_000)).status,
		"exactly the maximum is allowed")
}

// AC-7: two parallel top-ups that together exceed the maximum → exactly one is accepted.
func TestTopUp_ParallelMaxBalance(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.fund(t, p.userID, p.walletID, 9_800_000)
	results := make([]transferResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(150_000)) })
	}
	wg.Wait()
	codes := []int{results[0].status, results[1].status}
	assert.ElementsMatch(t, []int{http.StatusAccepted, http.StatusUnprocessableEntity}, codes)
	assert.Equal(t, 1, f.pendingTopUps(t))
}

// AC-4 through the real wiring: the outbox delivers the event and the fake bank accepts.
func TestTopUp_DeliveredToTheBank(t *testing.T) {
	f := newAPIFixture(t)
	p := f.topUpParty(t, "+201001234567")
	f.runOutbox(t)

	r := f.post(t, "/v1/topups", p.token, uuid.NewString(), topUpBody(50_000))
	require.Equal(t, http.StatusAccepted, r.status)
	txnID := r.body["transaction_id"].(string)

	require.Eventually(t, func() bool { s, _ := f.txnStatus(t, txnID); return s == "PROCESSING" }, 10*time.Second, 10*time.Millisecond)
	_, ref := f.txnStatus(t, txnID)
	assert.NotEmpty(t, ref)
	calls := f.bank.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, txnID, calls[0].Key.String())
	assert.Equal(t, testIBAN, calls[0].Debit.IBAN)
	require.Eventually(t, func() bool {
		return f.countWhere(t, `SELECT count(*) FROM outbox_events WHERE status = 'DONE'`) == 1
	}, 10*time.Second, 10*time.Millisecond)

	// The history API shows it as the user's top-up.
	got := f.call(t, http.MethodGet, "/v1/transactions/"+txnID, p.token, "")
	require.Equal(t, http.StatusOK, got.status, got.body)
	assert.Equal(t, "PROCESSING", got.body["status"])
}

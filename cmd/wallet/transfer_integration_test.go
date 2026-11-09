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
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

type party struct{ userID, walletID, token, phone string }

func (f apiFixture) newParty(t *testing.T, phone string) party {
	t.Helper()
	userID, walletID, token := f.registerAndLoginAs(t, phone)
	return party{userID: userID, walletID: walletID, token: token, phone: phone}
}

type transferResult struct {
	status   int
	body     map[string]any
	replayed bool
}

// transfer posts /v1/transfers with the given key ("" means no header).
func (f apiFixture) transfer(t *testing.T, token, key, body string) transferResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+"/v1/transfers", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
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

func transferBody(phone string, amount int64, note string) string {
	return fmt.Sprintf(`{"recipient_phone":%q,"amount_minor":%d,"currency":"EGP","note":%q}`, phone, amount, note)
}

func (f apiFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func (f apiFixture) freeze(t *testing.T, walletID string) {
	t.Helper()
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE wallets SET status = 'FROZEN' WHERE id = $1`, walletID)
	require.NoError(t, err)
}

func TestTransfer_Success(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 20_000)
	entriesBefore := f.count(t, "journal_entries")

	r := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 5_000, "  lunch "))

	require.Equal(t, http.StatusCreated, r.status, r.body)
	assert.False(t, r.replayed)
	assert.Equal(t, "P2P", r.body["type"])
	assert.Equal(t, "COMPLETED", r.body["status"])
	assert.EqualValues(t, 5_000, r.body["amount_minor"])
	assert.Equal(t, "EGP", r.body["currency"])
	assert.Equal(t, map[string]any{"phone": b.phone, "masked_name": "Ahmed M. A."}, r.body["recipient"])
	assert.Equal(t, "lunch", r.body["note"], "trimmed")
	assert.EqualValues(t, 15_000, r.body["balance_after_minor"])
	_, err := time.Parse(time.RFC3339Nano, r.body["created_at"].(string))
	require.NoError(t, err)

	assert.Equal(t, int64(15_000), f.balance(t, a.token))
	assert.Equal(t, int64(5_000), f.balance(t, b.token))

	txnID := r.body["transaction_id"].(string)
	var typ, status, src, dst string
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT type, status, source_wallet_id::text, destination_wallet_id::text FROM transactions WHERE id = $1`, txnID).Scan(&typ, &status, &src, &dst))
	assert.Equal(t, []string{"P2P", "COMPLETED", a.walletID, b.walletID}, []string{typ, status, src, dst}, "one transaction shared by both")
	var entries, postings int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(DISTINCT e.id), count(p.id)
		FROM journal_entries e JOIN postings p ON p.journal_entry_id = e.id
		WHERE e.transaction_id = $1 AND e.entry_type = 'P2P'`, txnID).Scan(&entries, &postings))
	assert.Equal(t, 1, entries)
	assert.Equal(t, 2, postings)
	assert.Equal(t, entriesBefore+1, f.count(t, "journal_entries"))

	noNote := f.transfer(t, a.token, uuid.NewString(), fmt.Sprintf(`{"recipient_phone":%q,"amount_minor":100,"currency":"EGP"}`, b.phone))
	require.Equal(t, http.StatusCreated, noNote.status)
	assert.Nil(t, noNote.body["note"], "absent note is null")
}

func TestTransfer_RecipientFrozenStillReceives(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)
	f.fund(t, b.userID, b.walletID, 10_000)
	f.freeze(t, b.walletID)

	r := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 1_000, ""))
	assert.Equal(t, http.StatusCreated, r.status, "a frozen recipient may receive (PRD-001 BR-8)")

	r = f.transfer(t, b.token, uuid.NewString(), transferBody(a.phone, 1_000, ""))
	assert.Equal(t, http.StatusForbidden, r.status)
	assert.Equal(t, "WALLET_FROZEN", r.body["error"].(map[string]any)["code"])
}

func TestTransfer_Errors(t *testing.T) {
	f := newAPIFixtureWith(t, clock.System{}, map[string]string{"LIMIT_DAILY_OUTGOING": "8000", "LIMIT_MAX_BALANCE": "12000"})
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)
	f.fund(t, b.userID, b.walletID, 9_000)
	poor := f.newParty(t, "+201001234569")
	frozen := f.newParty(t, "+201001234560")
	f.fund(t, frozen.userID, frozen.walletID, 5_000)
	f.freeze(t, frozen.walletID)

	key := uuid.NewString
	tests := []struct {
		name, token, key, body string
		status                 int
		code                   string
	}{
		{"1 no token", "", key(), transferBody(b.phone, 100, ""), 401, "UNAUTHENTICATED"},
		{"2 unknown field", a.token, key(), `{"recipient_phone":"+201001234568","amount_minor":100,"currency":"EGP","x":1}`, 400, "MALFORMED_REQUEST"},
		{"2 decimal amount", a.token, key(), `{"recipient_phone":"+201001234568","amount_minor":10.5,"currency":"EGP"}`, 400, "MALFORMED_REQUEST"},
		{"3 no key", a.token, "", transferBody(b.phone, 100, ""), 400, "IDEMPOTENCY_KEY_REQUIRED"},
		{"3 bad key", a.token, "not-a-uuid", transferBody(b.phone, 100, ""), 400, "IDEMPOTENCY_KEY_INVALID"},
		{"4 zero amount", a.token, key(), transferBody(b.phone, 0, ""), 400, "INVALID_AMOUNT"},
		{"5 currency", a.token, key(), `{"recipient_phone":"+201001234568","amount_minor":100,"currency":"USD"}`, 400, "UNSUPPORTED_CURRENCY"},
		{"6 phone", a.token, key(), transferBody("01001234568", 100, ""), 400, "INVALID_PHONE"},
		{"7 note", a.token, key(), transferBody(b.phone, 100, strings.Repeat("n", 141)), 400, "INVALID_NOTE"},
		{"8 below min", a.token, key(), transferBody(b.phone, 99, ""), 422, "AMOUNT_OUT_OF_RANGE"},
		{"9 unknown recipient", a.token, key(), transferBody("+201509999999", 100, ""), 404, "RECIPIENT_NOT_FOUND"},
		{"10 self", a.token, key(), transferBody(a.phone, 100, ""), 422, "SELF_TRANSFER_NOT_ALLOWED"},
		{"11 sender frozen", frozen.token, key(), transferBody(b.phone, 100, ""), 403, "WALLET_FROZEN"},
		{"12 insufficient", poor.token, key(), transferBody(b.phone, 100, ""), 422, "INSUFFICIENT_FUNDS"},
		{"13 daily limit", a.token, key(), transferBody(poor.phone, 8_001, ""), 422, "DAILY_LIMIT_EXCEEDED"},
		{"14 recipient limit", a.token, key(), transferBody(b.phone, 3_001, ""), 422, "RECIPIENT_LIMIT_EXCEEDED"},
	}
	txns, entries := f.count(t, "transactions"), f.count(t, "journal_entries")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.transfer(t, tt.token, tt.key, tt.body)
			assert.Equal(t, tt.status, r.status, r.body)
			assert.Equal(t, tt.code, r.body["error"].(map[string]any)["code"])
		})
	}
	assert.Equal(t, txns, f.count(t, "transactions"), "no transaction row for any error")
	assert.Equal(t, entries, f.count(t, "journal_entries"), "nothing posted for any error")
	assert.Equal(t, int64(10_000), f.balance(t, a.token))
}

func TestTransfer_IdempotentReplay(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)
	key := uuid.NewString()

	first := f.transfer(t, a.token, key, transferBody(b.phone, 1_000, "x"))
	second := f.transfer(t, a.token, key, transferBody(b.phone, 1_000, "x"))

	require.Equal(t, http.StatusCreated, first.status)
	assert.Equal(t, http.StatusCreated, second.status)
	assert.True(t, second.replayed)
	assert.Equal(t, first.body, second.body)
	assert.Equal(t, int64(9_000), f.balance(t, a.token), "one transfer")
}

func TestTransfer_KeyReused(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)
	key := uuid.NewString()
	require.Equal(t, http.StatusCreated, f.transfer(t, a.token, key, transferBody(b.phone, 1_000, "")).status)

	r := f.transfer(t, a.token, key, transferBody(b.phone, 2_000, ""))
	assert.Equal(t, http.StatusUnprocessableEntity, r.status)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", r.body["error"].(map[string]any)["code"])
}

func TestTransfer_BusinessErrorReplayed(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	key := uuid.NewString()
	first := f.transfer(t, a.token, key, transferBody(b.phone, 1_000, ""))
	require.Equal(t, http.StatusUnprocessableEntity, first.status)
	require.Equal(t, "INSUFFICIENT_FUNDS", first.body["error"].(map[string]any)["code"])

	f.fund(t, a.userID, a.walletID, 10_000)
	again := f.transfer(t, a.token, key, transferBody(b.phone, 1_000, ""))
	assert.Equal(t, http.StatusUnprocessableEntity, again.status, "the stored answer is replayed even after funding")
	assert.True(t, again.replayed)
	assert.Equal(t, first.body, again.body)
	assert.Equal(t, int64(10_000), f.balance(t, a.token))
}

func TestTransfer_ParallelDrainNeverNegative(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)

	var wg sync.WaitGroup
	results := make(chan transferResult, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 1_000, ""))
		}()
	}
	wg.Wait()
	close(results)
	ok, insufficient := 0, 0
	for r := range results {
		switch {
		case r.status == http.StatusCreated:
			ok++
		case r.status == http.StatusUnprocessableEntity && r.body["error"].(map[string]any)["code"] == "INSUFFICIENT_FUNDS":
			insufficient++
		default:
			t.Errorf("unexpected response %d %v", r.status, r.body)
		}
	}
	assert.Equal(t, 10, ok)
	assert.Equal(t, 10, insufficient)
	assert.Equal(t, int64(0), f.balance(t, a.token))
	assert.Equal(t, int64(10_000), f.balance(t, b.token))
}

func TestTransfer_OppositeDirectionsNoDeadlock(t *testing.T) {
	f := newAPIFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 100_000)
	f.fund(t, b.userID, b.walletID, 100_000)

	// 5 goroutines per direction, 10 transfers each: 50 A→B and 50 B→A at the same time.
	var wg sync.WaitGroup
	results := make(chan transferResult, 100)
	for _, dir := range []struct{ from, to party }{{a, b}, {b, a}} {
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					results <- f.transfer(t, dir.from.token, uuid.NewString(), transferBody(dir.to.phone, 100, ""))
				}
			}()
		}
	}
	wg.Wait()
	close(results)
	for r := range results {
		assert.Equal(t, http.StatusCreated, r.status, r.body)
	}
	assert.Zero(t, f.retries.count("deadlock"), "ordered locks: no deadlock happened (not even one retried away)")
	assert.Equal(t, int64(100_000), f.balance(t, a.token))
	assert.Equal(t, int64(100_000), f.balance(t, b.token))
}

func TestTransfer_DailyLimit_CairoDay(t *testing.T) {
	// 2026-11-10 in Cairo (UTC+2) starts at 2026-11-09T22:00:00Z.
	clk := clocktest.New(time.Date(2026, 11, 9, 21, 59, 0, 0, time.UTC))
	f := newAPIFixtureWith(t, clk, map[string]string{"LIMIT_DAILY_OUTGOING": "10000"})
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 50_000)

	require.Equal(t, http.StatusCreated, f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 9_000, "")).status, "yesterday")

	clk.Set(time.Date(2026, 11, 9, 22, 0, 0, 0, time.UTC)) // first instant of the new Cairo day
	require.Equal(t, http.StatusCreated, f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 4_000, "")).status, "yesterday's 9000 does not count")

	// A withdrawal still PENDING today counts too; a FAILED one does not.
	withdraw := func(amountMinor int64, key string, fail bool) {
		amount, err := money.NewPositive(amountMinor, money.EGP)
		require.NoError(t, err)
		require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			txn, err := f.txns.Create(ctx, tx, transaction.New{Type: transaction.TypeWithdrawal, Status: transaction.StatusPending,
				InitiatorUserID: uuid.MustParse(a.userID), SourceWalletID: uuid.MustParse(a.walletID), Amount: amount, IdempotencyKey: key})
			if err != nil || !fail {
				return err
			}
			_, err = f.txns.Transition(ctx, tx, txn.ID, transaction.StatusPending, transaction.StatusFailed, transaction.Patch{})
			return err
		}))
	}
	withdraw(3_000, "w-1", false)
	withdraw(5_000, "w-2", true)

	r := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 3_001, ""))
	assert.Equal(t, http.StatusUnprocessableEntity, r.status, "4000 + 3000 + 3001 > 10000")
	assert.Equal(t, "DAILY_LIMIT_EXCEEDED", r.body["error"].(map[string]any)["code"])
	assert.Equal(t, http.StatusCreated, f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 3_000, "")).status, "exactly the limit")
}

func TestTransfer_EarlierCheckWins(t *testing.T) {
	f := newAPIFixtureWith(t, clock.System{}, map[string]string{"LIMIT_DAILY_OUTGOING": "1000"})
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 500)

	// 2000 fails both check 12 (balance 500) and check 13 (daily limit 1000): 12 wins.
	r := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 2_000, ""))
	assert.Equal(t, "INSUFFICIENT_FUNDS", r.body["error"].(map[string]any)["code"])

	// An unknown recipient (9) with an amount below the minimum (8): 8 wins.
	r = f.transfer(t, a.token, uuid.NewString(), transferBody("+201509999999", 50, ""))
	assert.Equal(t, "AMOUNT_OUT_OF_RANGE", r.body["error"].(map[string]any)["code"])
}

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

const testInternalKey = "test-internal-api-key-of-32-bytes!"

func newOperatorFixture(t *testing.T) (apiFixture, *clocktest.Fake) {
	t.Helper()
	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	return newAPIFixtureWith(t, clk, map[string]string{"INTERNAL_API_KEY": testInternalKey}), clk
}

// internal calls an operator endpoint with key ("" = no header).
func (f apiFixture) internal(t *testing.T, method, path, key, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	if key != "" {
		req.Header.Set("X-Internal-Api-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return httpResult{status: resp.StatusCode, body: decoded}
}

func (f apiFixture) recordDiscrepancy(t *testing.T, clk *clocktest.Fake, kind discrepancy.Kind, event string) {
	t.Helper()
	svc := discrepancy.NewService(f.db.Pool(), clk, nil)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return svc.Record(ctx, tx, discrepancy.New{Kind: kind, Severity: discrepancy.SeverityWarning, Details: map[string]any{"event_id": event}})
	}))
	clk.Advance(time.Second)
}

// T026 AC-1.
func TestInternal_Auth(t *testing.T) {
	f, _ := newOperatorFixture(t)
	for _, path := range []string{"/internal/v1/discrepancies"} {
		assert.Equal(t, http.StatusUnauthorized, f.internal(t, http.MethodGet, path, "", "").status)
		r := f.internal(t, http.MethodGet, path, "wrong-key-wrong-key-wrong-key-wrong", "")
		assert.Equal(t, http.StatusUnauthorized, r.status)
		assert.Equal(t, "UNAUTHENTICATED", errorCode(r))
		assert.Equal(t, http.StatusOK, f.internal(t, http.MethodGet, path, testInternalKey, "").status)
	}
	assert.Equal(t, http.StatusUnauthorized, f.internal(t, http.MethodPost, "/internal/v1/wallets/"+uuid.NewString()+"/freeze", "", "").status)

	// Without INTERNAL_API_KEY the routes do not exist.
	plain := newAPIFixture(t)
	assert.Equal(t, http.StatusNotFound, plain.internal(t, http.MethodGet, "/internal/v1/discrepancies", testInternalKey, "").status)
}

// T026 AC-2.
func TestInternal_Discrepancies(t *testing.T) {
	f, clk := newOperatorFixture(t)
	for i := range 3 {
		f.recordDiscrepancy(t, clk, discrepancy.KindUnknownBankEvent, fmt.Sprintf("e-%d", i))
	}
	f.recordDiscrepancy(t, clk, discrepancy.KindLedgerIntegrity, "e-ledger")

	r := f.internal(t, http.MethodGet, "/internal/v1/discrepancies?limit=2", testInternalKey, "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	items := r.body["items"].([]any)
	require.Len(t, items, 2)
	first := items[0].(map[string]any)
	assert.Equal(t, "LEDGER_INTEGRITY", first["kind"], "newest first")
	assert.Nil(t, first["transaction_id"])
	assert.Equal(t, map[string]any{"event_id": "e-ledger"}, first["details"])
	cursor := r.body["next_cursor"].(string)

	r = f.internal(t, http.MethodGet, "/internal/v1/discrepancies?limit=2&cursor="+cursor, testInternalKey, "")
	require.Equal(t, http.StatusOK, r.status)
	assert.Len(t, r.body["items"], 2)
	assert.Nil(t, r.body["next_cursor"], "last page")

	r = f.internal(t, http.MethodGet, "/internal/v1/discrepancies?kind=UNKNOWN_BANK_EVENT&status=OPEN", testInternalKey, "")
	assert.Len(t, r.body["items"], 3)
	r = f.internal(t, http.MethodGet, "/internal/v1/discrepancies?status=RESOLVED", testInternalKey, "")
	assert.Empty(t, r.body["items"])

	for _, tt := range []struct{ query, code string }{
		{"status=CLOSED", "INVALID_FILTER"}, {"kind=OOPS", "INVALID_FILTER"},
		{"limit=0", "INVALID_LIMIT"}, {"limit=101", "INVALID_LIMIT"}, {"limit=x", "INVALID_LIMIT"},
		{"cursor=!!!", "INVALID_CURSOR"}, {"cursor=abc", "INVALID_CURSOR"},
	} {
		r := f.internal(t, http.MethodGet, "/internal/v1/discrepancies?"+tt.query, testInternalKey, "")
		assert.Equal(t, http.StatusBadRequest, r.status, tt.query)
		assert.Equal(t, tt.code, errorCode(r), tt.query)
	}
}

// T026 AC-3.
func TestInternal_Resolve(t *testing.T) {
	f, clk := newOperatorFixture(t)
	f.recordDiscrepancy(t, clk, discrepancy.KindUnknownBankEvent, "e-1")
	list := f.internal(t, http.MethodGet, "/internal/v1/discrepancies", testInternalKey, "")
	id := list.body["items"].([]any)[0].(map[string]any)["id"].(string)
	path := "/internal/v1/discrepancies/" + id + "/resolve"

	for _, body := range []string{`{"note":""}`, `{"note":"   "}`, `{"note":"` + strings.Repeat("x", 501) + `"}`} {
		r := f.internal(t, http.MethodPost, path, testInternalKey, body)
		assert.Equal(t, http.StatusBadRequest, r.status)
		assert.Equal(t, "INVALID_NOTE", errorCode(r))
	}
	r := f.internal(t, http.MethodPost, path, testInternalKey, `{"note":" checked with the bank: duplicate event "}`)
	require.Equal(t, http.StatusOK, r.status, r.body)
	assert.Equal(t, "RESOLVED", r.body["status"])
	assert.Equal(t, "checked with the bank: duplicate event", r.body["resolution_note"])
	assert.NotNil(t, r.body["resolved_at"])

	r = f.internal(t, http.MethodPost, path, testInternalKey, `{"note":"again"}`)
	assert.Equal(t, http.StatusConflict, r.status)
	assert.Equal(t, "ALREADY_RESOLVED", errorCode(r))
	for _, other := range []string{uuid.NewString(), "not-a-uuid"} {
		r = f.internal(t, http.MethodPost, "/internal/v1/discrepancies/"+other+"/resolve", testInternalKey, `{"note":"x"}`)
		assert.Equal(t, http.StatusNotFound, r.status)
		assert.Equal(t, "DISCREPANCY_NOT_FOUND", errorCode(r))
	}
}

// T026 AC-4.
func TestInternal_FreezeUnfreeze(t *testing.T) {
	f, _ := newOperatorFixture(t)
	a, b := f.newParty(t, "+201001234567"), f.newParty(t, "+201001234568")
	f.fund(t, a.userID, a.walletID, 10_000)
	f.fund(t, b.userID, b.walletID, 10_000)

	for range 2 { // idempotent
		r := f.internal(t, http.MethodPost, "/internal/v1/wallets/"+a.walletID+"/freeze", testInternalKey, "")
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.Equal(t, map[string]any{"wallet_id": a.walletID, "status": "FROZEN"}, r.body)
	}
	out := f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 1_000, ""))
	assert.Equal(t, http.StatusForbidden, out.status)
	assert.Equal(t, "WALLET_FROZEN", errorCode(httpResult{body: out.body}))
	in := f.transfer(t, b.token, uuid.NewString(), transferBody(a.phone, 1_000, ""))
	assert.Equal(t, http.StatusCreated, in.status, "a frozen wallet still receives (BR-8)")

	r := f.internal(t, http.MethodPost, "/internal/v1/wallets/"+a.walletID+"/unfreeze", testInternalKey, "")
	require.Equal(t, http.StatusOK, r.status)
	assert.Equal(t, "ACTIVE", r.body["status"])
	assert.Equal(t, http.StatusCreated, f.transfer(t, a.token, uuid.NewString(), transferBody(b.phone, 1_000, "")).status)

	for _, id := range []string{uuid.NewString(), "nope"} {
		r := f.internal(t, http.MethodPost, "/internal/v1/wallets/"+id+"/freeze", testInternalKey, "")
		assert.Equal(t, http.StatusNotFound, r.status)
		assert.Equal(t, "WALLET_NOT_FOUND", errorCode(r))
	}
}

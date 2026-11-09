package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

// fakeDB answers the INSERT with a fixed result.
type fakeDB struct {
	rows int64
	err  error

	mu      sync.Mutex
	inserts int
}

func (f *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserts++
	if f.err != nil {
		return pgconn.CommandTag{}, f.err
	}
	if f.rows == 1 {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 0"), nil
}

func (f *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakeDB: no queries")
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (f *fakeDB) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inserts
}

// discardRecorder accepts discrepancies and drops them.
type discardRecorder struct{}

func (discardRecorder) Record(context.Context, pgx.Tx, discrepancy.New) error { return nil }

// recorder counts webhook_received_total.
type recorder struct {
	mu      sync.Mutex
	results map[string]int
}

func (r *recorder) ObserveQueueClaimed(string, int)               {}
func (r *recorder) ObserveQueueJob(string, string, time.Duration) {}
func (r *recorder) ObserveWebhookReceived(result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results == nil {
		r.results = map[string]int{}
	}
	r.results[result]++
}

func (r *recorder) get(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results[result]
}

func newTestHandler(t *testing.T, db *fakeDB) (http.Handler, *recorder) {
	t.Helper()
	obs := &recorder{}
	clk := clocktest.New(now)
	svc, err := webhook.NewService(db, nil, discardRecorder{}, clk, webhook.Config{Workers: 1, MaxAttempts: 1, PollInterval: time.Second}, nil, obs)
	require.NoError(t, err)
	mux := http.NewServeMux()
	webhook.NewHandler(svc, secret, clk, nil, obs).Routes(mux)
	return mux, obs
}

func post(h http.Handler, payload []byte, sigHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/bank", bytes.NewReader(payload))
	if sigHeader != "" {
		req.Header.Set(webhook.SignatureHeader, sigHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func signed(payload []byte) string { return header(now.Unix(), sign(secret, now.Unix(), payload)) }

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b), rec.Body.String())
	return b.Error.Code
}

var event = []byte(`{"event_id":"e-1","event_type":"debit.succeeded","amount_minor":100}`)

func TestWebhook_StoredAndDuplicate(t *testing.T) {
	for _, tt := range []struct {
		rows int64
		want string
	}{{1, webhook.ReceivedStored}, {0, webhook.ReceivedDuplicate}} {
		db := &fakeDB{rows: tt.rows}
		h, obs := newTestHandler(t, db)
		rec := post(h, event, signed(event))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
		assert.Equal(t, 1, obs.get(tt.want))
	}
}

// AC-3 and AC-4 at the HTTP level: nothing reaches the database.
func TestWebhook_BadRequests(t *testing.T) {
	big := []byte(`{"event_id":"e-1","event_type":"debit.succeeded","pad":"` + strings.Repeat("a", httpserver.MaxBodyBytes) + `"}`)
	tests := []struct {
		name    string
		payload []byte
		header  string
		status  int
		code    string
		metric  string
	}{
		{"no signature", event, "", http.StatusUnauthorized, webhook.CodeInvalidSignature, webhook.ReceivedInvalidSignature},
		{"wrong secret", event, header(now.Unix(), sign([]byte("another-secret-of-32-bytes-long!"), now.Unix(), event)), http.StatusUnauthorized, webhook.CodeInvalidSignature, webhook.ReceivedInvalidSignature},
		{"tampered body", []byte(`{"event_id":"e-2","event_type":"debit.succeeded","amount_minor":100}`), signed(event), http.StatusUnauthorized, webhook.CodeInvalidSignature, webhook.ReceivedInvalidSignature},
		{"6 minutes old", event, header(now.Add(-6*time.Minute).Unix(), sign(secret, now.Add(-6*time.Minute).Unix(), event)), http.StatusUnauthorized, webhook.CodeInvalidSignature, webhook.ReceivedInvalidSignature},
		{"6 minutes in the future", event, header(now.Add(6*time.Minute).Unix(), sign(secret, now.Add(6*time.Minute).Unix(), event)), http.StatusUnauthorized, webhook.CodeInvalidSignature, webhook.ReceivedInvalidSignature},
		{"not JSON", []byte(`not json`), signed([]byte(`not json`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"JSON array", []byte(`[]`), signed([]byte(`[]`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"null", []byte(`null`), signed([]byte(`null`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"missing event_id", []byte(`{"event_type":"debit.succeeded"}`), signed([]byte(`{"event_type":"debit.succeeded"}`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"missing event_type", []byte(`{"event_id":"e-1"}`), signed([]byte(`{"event_id":"e-1"}`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"event_id too long", []byte(`{"event_id":"` + strings.Repeat("x", 201) + `","event_type":"t"}`), signed([]byte(`{"event_id":"` + strings.Repeat("x", 201) + `","event_type":"t"}`)), http.StatusBadRequest, httpserver.CodeMalformedRequest, webhook.ReceivedMalformed},
		{"larger than 64 KB", big, signed(big), http.StatusRequestEntityTooLarge, httpserver.CodeRequestTooLarge, webhook.ReceivedMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeDB{rows: 1}
			h, obs := newTestHandler(t, db)
			rec := post(h, tt.payload, tt.header)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			assert.Equal(t, tt.code, errorCode(t, rec))
			assert.Equal(t, 1, obs.get(tt.metric))
			assert.Zero(t, db.count(), "nothing stored")
		})
	}
}

// AC-4: the insert failed, so the bank must retry.
func TestWebhook_DBDown_503(t *testing.T) {
	db := &fakeDB{err: errors.New("connection refused")}
	h, obs := newTestHandler(t, db)
	rec := post(h, event, signed(event))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, httpserver.CodeTryAgain, errorCode(t, rec))
	assert.Equal(t, 1, obs.get(webhook.ReceivedError))
	assert.NotContains(t, rec.Body.String(), "connection refused")
}

func TestWebhook_OnlyPost(t *testing.T) {
	h, _ := newTestHandler(t, &fakeDB{rows: 1})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhooks/bank", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

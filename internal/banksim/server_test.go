package banksim_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/banksim"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

const (
	secret = "test-webhook-secret-of-32-bytes!"
	delay  = 20 * time.Millisecond
)

var t0 = time.Date(2025, 11, 9, 10, 0, 0, 0, time.UTC) // unix 1762682400

func TestSign_KnownVector(t *testing.T) {
	// Computed independently: python3 hmac.new(secret, b"1762682400." + body, sha256).hexdigest()
	got := banksim.Sign([]byte(secret), t0, []byte(`{"event_id":"x"}`))

	assert.Equal(t, "t=1762682400,v1=b6625751ed23392018d345cd665e5ef0b4664c0660f71789ed110eaafe0e3bb8", got)
}

// delivery is one webhook call received by the fake wallet.
type delivery struct {
	event     banksim.Event
	raw       []byte
	signature string
}

// wallet is a fake wallet webhook endpoint. Its answers can be scripted (default 200).
type wallet struct {
	mu        sync.Mutex
	got       []delivery
	answers   []int
	received  chan delivery
	onRequest func()
}

func newWallet(t *testing.T, answers ...int) (*wallet, string) {
	t.Helper()
	w := &wallet{answers: answers, received: make(chan delivery, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var ev banksim.Event
		require.NoError(t, json.Unmarshal(raw, &ev))
		d := delivery{event: ev, raw: raw, signature: r.Header.Get(banksim.SignatureHeader)}
		w.mu.Lock()
		w.got = append(w.got, d)
		status := http.StatusOK
		if len(w.answers) > 0 {
			status, w.answers = w.answers[0], w.answers[1:]
		}
		hook := w.onRequest
		w.mu.Unlock()
		if hook != nil {
			hook()
		}
		rw.WriteHeader(status)
		w.received <- d
	}))
	t.Cleanup(srv.Close)
	return w, srv.URL + "/webhooks/bank"
}

func (w *wallet) next(t *testing.T) delivery {
	t.Helper()
	select {
	case d := <-w.received:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook received")
		return delivery{}
	}
}

func (w *wallet) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.got)
}

type sim struct {
	url   string
	clock *clocktest.Fake
}

// newSim starts the simulator with a fake clock and short delays; it stops when the test ends.
func newSim(t *testing.T, webhookURL string) sim {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clk := clocktest.New(t0)
	s := banksim.New(ctx, banksim.Config{
		WebhookURL: webhookURL, WebhookSecret: []byte(secret), Delay: delay,
		RetryDelays: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond},
	}, clk, nil)
	mux := http.NewServeMux()
	s.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		cancel()
		s.Wait()
	})
	return sim{url: srv.URL, clock: clk}
}

type createBody struct {
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	IBAN        string    `json:"iban"`
	HolderName  string    `json:"holder_name"`
	Reference   uuid.UUID `json:"reference"`
}

func (s sim) post(t *testing.T, path string, key string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, s.url+path, bytes.NewReader(raw))
	require.NoError(t, err)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return do(t, req)
}

func (s sim) get(t *testing.T, key uuid.UUID) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.url+"/v1/operations/"+key.String(), nil)
	require.NoError(t, err)
	return do(t, req)
}

func do(t *testing.T, req *http.Request) (int, map[string]any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	}
	return resp.StatusCode, out
}

func body(key uuid.UUID, iban string, amount int64) createBody {
	return createBody{AmountMinor: amount, Currency: "EGP", IBAN: iban, HolderName: "Ahmed Ali", Reference: key}
}

// verify checks the signature of a delivery like the wallet will (TDR-007 §1).
func verify(t *testing.T, d delivery) int64 {
	t.Helper()
	parts := strings.Split(d.signature, ",")
	require.Len(t, parts, 2, d.signature)
	ts, ok := strings.CutPrefix(parts[0], "t=")
	require.True(t, ok)
	sig, ok := strings.CutPrefix(parts[1], "v1=")
	require.True(t, ok)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(d.raw)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), sig, "signature")
	unix, err := strconv.ParseInt(ts, 10, 64)
	require.NoError(t, err)
	return unix
}

func TestBanksim_Scenarios(t *testing.T) {
	tests := []struct {
		path, kind, iban     string
		eventType, status    string
		failureCode, failMsg string
	}{
		{"/v1/debits", "DEBIT", "EG380019000500000000263180000", "debit.succeeded", "SUCCEEDED", "", ""},
		{"/v1/payouts", "PAYOUT", "EG380019000500000000263180000", "payout.succeeded", "SUCCEEDED", "", ""},
		{"/v1/debits", "DEBIT", "EG380019000500000000263180001", "debit.failed", "FAILED", "INSUFFICIENT_FUNDS", "Insufficient funds in the source account."},
		{"/v1/payouts", "PAYOUT", "EG380019000500000000263180001", "payout.failed", "FAILED", "ACCOUNT_CLOSED", "The destination account is closed."},
		{"/v1/debits", "DEBIT", "EG3800190005000000002631807", "debit.succeeded", "SUCCEEDED", "", ""}, // other suffix → 0000
	}
	for _, tt := range tests {
		t.Run(tt.eventType+"/"+tt.iban[len(tt.iban)-4:], func(t *testing.T) {
			w, hook := newWallet(t)
			s := newSim(t, hook)
			key := uuid.New()

			code, resp := s.post(t, tt.path, key.String(), body(key, tt.iban, 15_000))

			require.Equal(t, http.StatusAccepted, code, resp)
			ref, _ := resp["bank_reference"].(string)
			assert.True(t, strings.HasPrefix(ref, "BSIM-"), ref)
			assert.Equal(t, "PENDING", resp["status"])
			_, op := s.get(t, key)
			assert.Equal(t, "PENDING", op["status"], "PENDING until the delay")

			d := w.next(t)
			assert.Equal(t, t0.Unix(), verify(t, d))
			ev := d.event
			assert.NotEqual(t, uuid.Nil, ev.EventID)
			assert.Equal(t, tt.eventType, ev.EventType)
			assert.Equal(t, ref, ev.BankReference)
			assert.Equal(t, key, ev.IdempotencyKey)
			assert.Equal(t, int64(15_000), ev.AmountMinor)
			assert.Equal(t, "EGP", ev.Currency)
			assert.Equal(t, tt.status, ev.Status)
			assert.Equal(t, tt.failureCode, ev.FailureCode)
			assert.Equal(t, tt.failMsg, ev.FailureMessage)
			assert.True(t, ev.OccurredAt.Equal(t0))

			code, op = s.get(t, key)
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, map[string]any{
				"idempotency_key": key.String(), "bank_reference": ref, "kind": tt.kind, "status": tt.status,
				"amount_minor": float64(15_000), "currency": "EGP",
			}, without(op, "failure_code", "failure_message"))
			if tt.failureCode != "" {
				assert.Equal(t, tt.failureCode, op["failure_code"])
				assert.Equal(t, tt.failMsg, op["failure_message"])
			}
			time.Sleep(5 * delay)
			assert.Equal(t, 1, w.count(), "one webhook")
		})
	}
}

func without(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func TestBanksim_InvalidAccount(t *testing.T) {
	w, hook := newWallet(t)
	s := newSim(t, hook)
	key := uuid.New()
	b := body(key, "EG380019000500000000263180002", 500)

	for range 2 { // the same key again → the same 422
		code, resp := s.post(t, "/v1/payouts", key.String(), b)
		assert.Equal(t, http.StatusUnprocessableEntity, code)
		assert.Equal(t, map[string]any{"error": map[string]any{"code": "INVALID_ACCOUNT", "message": "The bank account is not valid."}}, resp)
	}
	code, resp := s.get(t, key)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "NOT_FOUND", resp["error"].(map[string]any)["code"])
	time.Sleep(5 * delay)
	assert.Equal(t, 0, w.count(), "no webhook")
}

func TestBanksim_Idempotency(t *testing.T) {
	w, hook := newWallet(t)
	s := newSim(t, hook)
	key := uuid.New()
	b := body(key, "EG380019000500000000263180000", 700)

	code1, first := s.post(t, "/v1/debits", key.String(), b)
	code2, second := s.post(t, "/v1/debits", key.String(), b)
	require.Equal(t, http.StatusAccepted, code1)
	assert.Equal(t, http.StatusAccepted, code2)
	assert.Equal(t, first, second, "same key + same body → the original response")

	changed := b
	changed.AmountMinor = 701
	code, resp := s.post(t, "/v1/debits", key.String(), changed)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", resp["error"].(map[string]any)["code"])
	code, resp = s.post(t, "/v1/payouts", key.String(), b)
	assert.Equal(t, http.StatusUnprocessableEntity, code, "same body on the other kind is a different request")
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", resp["error"].(map[string]any)["code"])

	w.next(t)
	time.Sleep(5 * delay)
	assert.Equal(t, 1, w.count(), "only one webhook")
}

func TestBanksim_AdminOverrideOneShot(t *testing.T) {
	w, hook := newWallet(t)
	s := newSim(t, hook)
	iban := "EG380019000500000000263180000"

	code, _ := s.post(t, "/admin/scenarios", "", map[string]string{"iban": iban, "scenario": "0001"})
	require.Equal(t, http.StatusNoContent, code)

	k1, k2 := uuid.New(), uuid.New()
	code, _ = s.post(t, "/v1/debits", k1.String(), body(k1, iban, 100))
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "debit.failed", w.next(t).event.EventType, "the next request uses the override")

	code, _ = s.post(t, "/v1/debits", k2.String(), body(k2, iban, 100))
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "debit.succeeded", w.next(t).event.EventType, "the one after uses the suffix again")

	code, _ = s.post(t, "/admin/scenarios", "", map[string]string{"iban": iban, "scenario": "0002"})
	require.Equal(t, http.StatusNoContent, code)
	k3 := uuid.New()
	code, _ = s.post(t, "/v1/debits", k3.String(), body(k3, iban, 100))
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	for _, bad := range []map[string]string{{"iban": iban, "scenario": "0005"}, {"iban": "", "scenario": "0000"}, {"scenario": "0000"}} {
		code, resp := s.post(t, "/admin/scenarios", "", bad)
		assert.Equal(t, http.StatusBadRequest, code, bad)
		assert.Equal(t, "INVALID_REQUEST", resp["error"].(map[string]any)["code"])
	}
}

func TestNotifier_Retries(t *testing.T) {
	w, hook := newWallet(t, http.StatusInternalServerError, http.StatusInternalServerError, http.StatusOK)
	s := newSim(t, hook)
	w.onRequest = func() { s.clock.Advance(time.Second) } // each retry is signed later
	key := uuid.New()

	code, _ := s.post(t, "/v1/payouts", key.String(), body(key, "EG0000", 100))
	require.Equal(t, http.StatusAccepted, code)

	first, second, third := w.next(t), w.next(t), w.next(t)
	assert.Equal(t, first.event.EventID, second.event.EventID)
	assert.Equal(t, first.event.EventID, third.event.EventID, "a retry reuses the event_id")
	assert.Equal(t, first.raw, third.raw)
	t1, t2, t3 := verify(t, first), verify(t, second), verify(t, third)
	assert.Less(t, t1, t2, "re-signed with a fresh t")
	assert.Less(t, t2, t3)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 3, w.count(), "delivered on the 3rd attempt, then stops")
}

func TestNotifier_GivesUp(t *testing.T) {
	w, hook := newWallet(t, 500, 500, 500, 500, 500, 500)
	s := newSim(t, hook)
	key := uuid.New()

	code, _ := s.post(t, "/v1/debits", key.String(), body(key, "EG0000", 100))
	require.Equal(t, http.StatusAccepted, code)

	for range 4 { // 1 attempt + 3 retries in the test config
		w.next(t)
	}
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 4, w.count())
	_, op := s.get(t, key)
	assert.Equal(t, "SUCCEEDED", op["status"], "the status changed before the first attempt")
}

func TestBanksim_ShutdownStopsPendingWork(t *testing.T) {
	w, hook := newWallet(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := banksim.New(ctx, banksim.Config{WebhookURL: hook, WebhookSecret: []byte(secret), Delay: time.Hour}, clocktest.New(t0), nil)
	mux := http.NewServeMux()
	s.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	key := uuid.New()
	code, _ := sim{url: srv.URL}.post(t, "/v1/debits", key.String(), body(key, "EG0000", 100))
	require.Equal(t, http.StatusAccepted, code)

	cancel()
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}
	assert.Equal(t, 0, w.count(), "a pending webhook is dropped")
}

func TestBanksim_Validation(t *testing.T) {
	_, hook := newWallet(t)
	s := newSim(t, hook)
	key := uuid.New()
	valid := body(key, "EG0000", 100)
	mutate := func(f func(*createBody)) createBody { b := valid; f(&b); return b }

	tests := []struct {
		name string
		key  string
		body any
	}{
		{"no key", "", valid},
		{"key not a UUID", "abc", valid},
		{"zero amount", key.String(), mutate(func(b *createBody) { b.AmountMinor = 0 })},
		{"negative amount", key.String(), mutate(func(b *createBody) { b.AmountMinor = -5 })},
		{"USD", key.String(), mutate(func(b *createBody) { b.Currency = "USD" })},
		{"no iban", key.String(), mutate(func(b *createBody) { b.IBAN = " " })},
		{"no holder", key.String(), mutate(func(b *createBody) { b.HolderName = "" })},
		{"reference ≠ key", key.String(), mutate(func(b *createBody) { b.Reference = uuid.New() })},
		{"unknown field", key.String(), map[string]any{"amount_minor": 1, "currency": "EGP", "iban": "x", "holder_name": "y", "reference": key, "extra": 1}},
		{"fractional amount", key.String(), map[string]any{"amount_minor": 1.5, "currency": "EGP", "iban": "x", "holder_name": "y", "reference": key}},
		{"not an object", key.String(), []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resp := s.post(t, "/v1/debits", tt.key, tt.body)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Equal(t, "INVALID_REQUEST", resp["error"].(map[string]any)["code"])
		})
	}
	code, _ := s.get(t, key)
	assert.Equal(t, http.StatusNotFound, code, "invalid requests record nothing")

	req, err := http.NewRequest(http.MethodGet, s.url+"/v1/operations/not-a-uuid", nil)
	require.NoError(t, err)
	code, resp := do(t, req)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_REQUEST", resp["error"].(map[string]any)["code"])
}

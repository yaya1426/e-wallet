package banksim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/bank/banksim"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

type observation struct {
	op, result string
	duration   time.Duration
}

type observer struct {
	mu  sync.Mutex
	got []observation
}

func (o *observer) ObserveBankCall(op, result string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.got = append(o.got, observation{op, result, d})
}

func newClient(t *testing.T, h http.HandlerFunc, timeout time.Duration) (*banksim.Client, *observer, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	obs := &observer{}
	var logs bytes.Buffer
	c, err := banksim.New(banksim.Config{BaseURL: srv.URL, Timeout: timeout},
		slog.New(slog.NewJSONHandler(&logs, nil)), obs)
	require.NoError(t, err)
	return c, obs, &logs
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(minor, money.EGP)
	require.NoError(t, err)
	return m
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

type captured struct {
	method, path, key, contentType string
	body                           map[string]any
}

func capture(into *captured, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		into.method, into.path = r.Method, r.URL.Path
		into.key, into.contentType = r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &into.body)
		}
		respond(status, body)(w, r)
	}
}

func TestClient_CreateDebit(t *testing.T) {
	var got captured
	c, _, _ := newClient(t, capture(&got, http.StatusAccepted, `{"bank_reference":"BNK-1","status":"PENDING"}`), time.Second)
	key := uuid.New()

	acc, err := c.CreateDebit(context.Background(), bank.DebitRequest{
		IdempotencyKey: key, Amount: egp(t, 150_000), IBAN: "EG380019000500000000263180002", HolderName: "Ahmed Ali",
	})

	require.NoError(t, err)
	assert.Equal(t, bank.Acceptance{BankReference: "BNK-1"}, acc)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v1/debits", got.path)
	assert.Equal(t, key.String(), got.key)
	assert.Equal(t, "application/json", got.contentType)
	assert.Equal(t, map[string]any{
		"amount_minor": float64(150_000), "currency": "EGP", "iban": "EG380019000500000000263180002",
		"holder_name": "Ahmed Ali", "reference": key.String(),
	}, got.body)
}

func TestClient_CreatePayout(t *testing.T) {
	var got captured
	c, _, _ := newClient(t, capture(&got, http.StatusAccepted, `{"bank_reference":"BNK-2","status":"PENDING"}`), time.Second)
	key := uuid.New()

	acc, err := c.CreatePayout(context.Background(), bank.PayoutRequest{
		IdempotencyKey: key, Amount: egp(t, 2500), IBAN: "EG1", HolderName: "Mona",
	})

	require.NoError(t, err)
	assert.Equal(t, "BNK-2", acc.BankReference)
	assert.Equal(t, "/v1/payouts", got.path)
	assert.Equal(t, key.String(), got.key)
	assert.Equal(t, float64(2500), got.body["amount_minor"])
	assert.Equal(t, key.String(), got.body["reference"])
}

func TestClient_GetOperation(t *testing.T) {
	key := uuid.New()
	var got captured
	c, _, _ := newClient(t, capture(&got, http.StatusOK, `{"idempotency_key":"`+key.String()+`","bank_reference":"BNK-3",
		"kind":"PAYOUT","status":"FAILED","amount_minor":99,"currency":"EGP",
		"failure_code":"ACCOUNT_CLOSED","failure_message":"The account is closed"}`), time.Second)

	op, err := c.GetOperation(context.Background(), key)

	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v1/operations/"+key.String(), got.path)
	assert.Empty(t, got.key, "a read sends no Idempotency-Key")
	assert.Equal(t, bank.Operation{
		IdempotencyKey: key, BankReference: "BNK-3", Kind: bank.KindPayout, Status: bank.StatusFailed,
		Amount: egp(t, 99), FailureCode: "ACCOUNT_CLOSED", FailureMessage: "The account is closed",
	}, op)
}

func TestClient_GetOperation_InvalidBodiesAreTemporary(t *testing.T) {
	key := uuid.New().String()
	valid := map[string]any{"idempotency_key": key, "bank_reference": "B", "kind": "DEBIT", "status": "SUCCEEDED", "amount_minor": 1, "currency": "EGP"}
	tests := map[string]func(map[string]any){
		"currency USD":     func(b map[string]any) { b["currency"] = "USD" },
		"unknown kind":     func(b map[string]any) { b["kind"] = "REFUND" },
		"unknown status":   func(b map[string]any) { b["status"] = "DONE" },
		"no reference":     func(b map[string]any) { delete(b, "bank_reference") },
		"no key":           func(b map[string]any) { delete(b, "idempotency_key") },
		"string amount":    func(b map[string]any) { b["amount_minor"] = "1" },
		"fractional minor": func(b map[string]any) { b["amount_minor"] = 1.5 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range valid {
				body[k] = v
			}
			mutate(body)
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			c, _, _ := newClient(t, respond(http.StatusOK, string(raw)), time.Second)

			_, err = c.GetOperation(context.Background(), uuid.MustParse(key))

			assert.ErrorIs(t, err, bank.ErrTemporary)
		})
	}
}

func TestClient_ErrorMapping(t *testing.T) {
	type want struct {
		temporary, notFound bool
		rejected            *bank.RejectedError
	}
	tests := []struct {
		name   string
		status int
		body   string
		create want // CreateDebit and CreatePayout
		get    want // GetOperation
	}{
		{"404", 404, `{"error":{"code":"NOT_FOUND","message":"no such operation"}}`,
			want{rejected: &bank.RejectedError{Code: "NOT_FOUND", Message: "no such operation"}}, want{notFound: true}},
		{"408", 408, `{"error":{"code":"TIMEOUT"}}`, want{temporary: true}, want{temporary: true}},
		{"429", 429, ``, want{temporary: true}, want{temporary: true}},
		{"422 with code", 422, `{"error":{"code":"INVALID_ACCOUNT","message":"IBAN does not exist"}}`,
			want{rejected: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "IBAN does not exist"}},
			want{rejected: &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "IBAN does not exist"}}},
		{"400 without body", 400, ``,
			want{rejected: &bank.RejectedError{Code: "BANK_REJECTED", Message: "Bad Request"}},
			want{rejected: &bank.RejectedError{Code: "BANK_REJECTED", Message: "Bad Request"}}},
		{"409 unparsable body", 409, `<html>conflict</html>`,
			want{rejected: &bank.RejectedError{Code: "BANK_REJECTED", Message: "Conflict"}},
			want{rejected: &bank.RejectedError{Code: "BANK_REJECTED", Message: "Conflict"}}},
		{"500", 500, `{"error":{"code":"INTERNAL"}}`, want{temporary: true}, want{temporary: true}},
		{"503", 503, ``, want{temporary: true}, want{temporary: true}},
		{"2xx unparsable", 202, `not json`, want{temporary: true}, want{temporary: true}},
		{"202 without reference", 202, `{"status":"PENDING"}`, want{temporary: true}, want{temporary: true}},
		{"unexpected 200 on create", 200, `{"bank_reference":"B"}`, want{temporary: true}, want{temporary: true}},
		{"redirect", 302, ``, want{temporary: true}, want{temporary: true}},
	}
	check := func(t *testing.T, err error, w want) {
		t.Helper()
		require.Error(t, err)
		var rejected *bank.RejectedError
		switch {
		case w.rejected != nil:
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, w.rejected, rejected)
			assert.NotErrorIs(t, err, bank.ErrTemporary)
		case w.notFound:
			assert.ErrorIs(t, err, bank.ErrNotFound)
		case w.temporary:
			assert.ErrorIs(t, err, bank.ErrTemporary)
			assert.False(t, errors.As(err, &rejected))
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newClient(t, respond(tt.status, tt.body), time.Second)
			ctx := context.Background()

			_, err := c.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})
			check(t, err, tt.create)
			_, err = c.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})
			check(t, err, tt.create)
			if tt.status == 200 { // a 200 is the success answer of GetOperation; covered elsewhere
				return
			}
			_, err = c.GetOperation(ctx, uuid.New())
			check(t, err, tt.get)
		})
	}
}

func TestClient_ConnectionErrorIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	c, err := banksim.New(banksim.Config{BaseURL: url, Timeout: time.Second}, nil, nil)
	require.NoError(t, err)

	_, err = c.CreateDebit(context.Background(), bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})

	assert.ErrorIs(t, err, bank.ErrTemporary)
}

func TestClient_OversizedBodyIsTemporary(t *testing.T) {
	big := `{"bank_reference":"B","pad":"` + strings.Repeat("x", 70<<10) + `"}`
	c, _, _ := newClient(t, respond(http.StatusAccepted, big), time.Second)

	_, err := c.CreateDebit(context.Background(), bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})

	assert.ErrorIs(t, err, bank.ErrTemporary)
}

func TestClient_Timeout(t *testing.T) {
	release := make(chan struct{})
	c, _, _ := newClient(t, func(http.ResponseWriter, *http.Request) { <-release }, 200*time.Millisecond)
	// Registered after the server's Close, so it runs first: Close waits for the handler.
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := c.CreateDebit(context.Background(), bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, bank.ErrTemporary)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond)
	assert.Less(t, elapsed, time.Second, "fails at about BANK_HTTP_TIMEOUT")
}

func TestClient_CallerContextCanceled(t *testing.T) {
	c, _, _ := newClient(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.GetOperation(ctx, uuid.New())

	assert.ErrorIs(t, err, bank.ErrTemporary)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestClient_Metrics(t *testing.T) {
	var calls int
	c, obs, logs := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			respond(http.StatusAccepted, `{"bank_reference":"B"}`)(w, r)
		case 2:
			respond(http.StatusUnprocessableEntity, `{"error":{"code":"INVALID_ACCOUNT"}}`)(w, r)
		case 3:
			respond(http.StatusServiceUnavailable, ``)(w, r)
		default:
			respond(http.StatusNotFound, ``)(w, r)
		}
	}, time.Second)
	ctx := context.Background()
	key := uuid.New()

	_, _ = c.CreateDebit(ctx, bank.DebitRequest{IdempotencyKey: key, Amount: egp(t, 1)})
	_, _ = c.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key, Amount: egp(t, 1)})
	_, _ = c.CreatePayout(ctx, bank.PayoutRequest{IdempotencyKey: key, Amount: egp(t, 1)})
	_, _ = c.GetOperation(ctx, key)

	require.Len(t, obs.got, 4)
	labels := make([][2]string, len(obs.got))
	for i, o := range obs.got {
		labels[i] = [2]string{o.op, o.result}
		assert.Positive(t, o.duration)
	}
	assert.Equal(t, [][2]string{
		{banksim.OpCreateDebit, banksim.ResultOK},
		{banksim.OpCreatePayout, banksim.ResultRejected},
		{banksim.OpCreatePayout, banksim.ResultTemporary},
		{banksim.OpGetOperation, banksim.ResultNotFound},
	}, labels)
	assert.Contains(t, logs.String(), `"bank_op":"create_debit"`)
	assert.Contains(t, logs.String(), `"idempotency_key":"`+key.String()+`"`)
	assert.Contains(t, logs.String(), `"status":"temporary"`)
	assert.Contains(t, logs.String(), `"duration"`)
}

func TestNew_InvalidConfig(t *testing.T) {
	for _, base := range []string{"", "banksim:9090", "ftp://x", "http://", "/v1"} {
		_, err := banksim.New(banksim.Config{BaseURL: base, Timeout: time.Second}, nil, nil)
		assert.Error(t, err, base)
	}
	_, err := banksim.New(banksim.Config{BaseURL: "http://b", Timeout: 0}, nil, nil)
	assert.Error(t, err)
}

func TestClient_BaseURLWithPath(t *testing.T) {
	var got captured
	srv := httptest.NewServer(capture(&got, http.StatusAccepted, `{"bank_reference":"B"}`))
	t.Cleanup(srv.Close)
	c, err := banksim.New(banksim.Config{BaseURL: srv.URL + "/bank/", Timeout: time.Second}, nil, nil)
	require.NoError(t, err)

	_, err = c.CreateDebit(context.Background(), bank.DebitRequest{IdempotencyKey: uuid.New(), Amount: egp(t, 1)})

	require.NoError(t, err)
	assert.Equal(t, "/bank/v1/debits", got.path)
}

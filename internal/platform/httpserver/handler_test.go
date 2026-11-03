package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/yaya1426/e-wallet/internal/platform/logger"
)

type observation struct {
	method, route string
	status        int
}

type fakeObserver struct {
	mu   sync.Mutex
	seen []observation
}

func (o *fakeObserver) ObserveHTTP(method, route string, status int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, observation{method, route, status})
}

// newTestHandler returns the middleware chain around a mux with a few test routes, and
// the buffer the logger writes to.
func newTestHandler(t *testing.T, observer Observer) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	log := logger.New(&logs, slog.LevelDebug)

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", Healthz())
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	return NewHandler(mux, log, observer), &logs
}

func decodeError(t *testing.T, body io.Reader) errorDetail {
	t.Helper()
	var b errorBody
	require.NoError(t, json.NewDecoder(body).Decode(&b))
	return b.Error
}

func TestRequestID_PropagatesIncoming(t *testing.T) {
	h, logs := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/items/1", nil)
	req.Header.Set(HeaderRequestID, "abc")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, "abc", rec.Header().Get(HeaderRequestID))
	assert.Contains(t, logs.String(), `"request_id":"abc"`)
	assert.Contains(t, logs.String(), `"msg":"http request"`)
}

func TestRequestID_GeneratesWhenMissing(t *testing.T) {
	h, logs := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/items/1", nil))

	id := rec.Header().Get(HeaderRequestID)
	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), parsed.Version())
	assert.Contains(t, logs.String(), `"request_id":"`+id+`"`)
}

func TestRequestID_ReplacesInvalidIncoming(t *testing.T) {
	for _, bad := range []string{"has space", "new\nline", strings.Repeat("a", maxRequestIDLength+1)} {
		h, _ := newTestHandler(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set(HeaderRequestID, bad)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		got := rec.Header().Get(HeaderRequestID)
		assert.NotEqual(t, bad, got)
		_, err := uuid.Parse(got)
		assert.NoError(t, err)
	}
}

func TestRouter_NotFound(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	e := decodeError(t, rec.Body)
	assert.Equal(t, CodeNotFound, e.Code)
	assert.NotEmpty(t, e.Message)
	assert.Equal(t, rec.Header().Get(HeaderRequestID), e.RequestID)
}

func TestRouter_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Header().Get("Allow"), http.MethodGet)
	assert.Equal(t, CodeMethodNotAllowed, decodeError(t, rec.Body).Code)
}

func TestRouter_RoutedRequestUnchanged(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"ok":"yes"}`, rec.Body.String())
}

func TestRecover_PanicReturns500(t *testing.T) {
	h, logs := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	e := decodeError(t, rec.Body)
	assert.Equal(t, CodeInternal, e.Code)
	assert.NotContains(t, rec.Body.String(), "boom", "panic value must not leak to the client")
	assert.Contains(t, logs.String(), "panic in http handler")
	assert.Contains(t, logs.String(), "goroutine", "stack trace is logged")
}

func TestRecover_ReraisesAbortHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	h := NewHandler(mux, logger.New(io.Discard, slog.LevelInfo), nil)

	assert.PanicsWithError(t, http.ErrAbortHandler.Error(), func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	})
}

func TestAccessLog_ReportsRoutePatternToObserver(t *testing.T) {
	obs := &fakeObserver{}
	h, _ := newTestHandler(t, obs)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items/7", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))

	assert.Equal(t, []observation{
		{http.MethodGet, "GET /v1/items/{id}", http.StatusOK},
		{http.MethodGet, routeUnmatched, http.StatusNotFound},
		{http.MethodGet, "GET /panic", http.StatusInternalServerError},
	}, obs.seen)
}

func TestAccessLog_HealthProbesLoggedAtDebug(t *testing.T) {
	var logs bytes.Buffer
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", Healthz())
	h := NewHandler(mux, logger.New(&logs, slog.LevelInfo), nil)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Empty(t, logs.String())
}

func TestHealthz_OK(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func check(name string, status CheckStatus) ReadinessCheck {
	return ReadinessCheck{Name: name, Check: func(context.Context) CheckStatus { return status }}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		checks     []ReadinessCheck
		wantStatus int
		wantBody   string
	}{
		{
			name:       "all ok",
			checks:     []ReadinessCheck{check("database", CheckOK), check("migrations", CheckOK)},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}`,
		},
		{
			name:       "database down",
			checks:     []ReadinessCheck{check("database", CheckFail), check("migrations", CheckFail)},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","checks":{"database":"fail","migrations":"fail"}}`,
		},
		{
			name:       "migrations pending",
			checks:     []ReadinessCheck{check("database", CheckOK), check("migrations", CheckPending)},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","checks":{"database":"ok","migrations":"pending"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			Readyz(time.Second, tt.checks...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestReadyz_CheckGetsTimeout(t *testing.T) {
	slow := ReadinessCheck{Name: "database", Check: func(ctx context.Context) CheckStatus {
		<-ctx.Done()
		return CheckFail
	}}
	rec := httptest.NewRecorder()
	start := time.Now()

	Readyz(50*time.Millisecond, slow).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAccessLog_NonStandardMethodObservedAsOther(t *testing.T) {
	obs := &fakeObserver{}
	h, _ := newTestHandler(t, obs)

	for _, method := range []string{"FOO1", "FOO2", http.MethodPost} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/x", nil))
	}

	assert.Equal(t, []observation{
		{methodOther, routeUnmatched, http.StatusNotFound},
		{methodOther, routeUnmatched, http.StatusNotFound},
		{http.MethodPost, routeUnmatched, http.StatusNotFound},
	}, obs.seen)
}

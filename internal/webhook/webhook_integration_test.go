//go:build integration

package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/banksim"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

const (
	waitFor = 10 * time.Second
	tick    = 10 * time.Millisecond
)

// spyHandler records the events it handles. Each call writes a row in handler_writes in the
// worker's transaction, then returns the next scripted error (nil when the script is empty).
type spyHandler struct {
	mu     sync.Mutex
	events []webhook.BankEvent
	errs   []error
}

func (h *spyHandler) HandleBankEvent(ctx context.Context, tx pgx.Tx, ev webhook.BankEvent) error {
	if tx == nil {
		return errors.New("no transaction")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO handler_writes (event_id) VALUES ($1)`, ev.EventID); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, ev)
	if len(h.errs) == 0 {
		return nil
	}
	err := h.errs[0]
	h.errs = h.errs[1:]
	return err
}

func (h *spyHandler) calls() []webhook.BankEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]webhook.BankEvent(nil), h.events...)
}

type fixture struct {
	db    *database.DB
	clock *clocktest.Fake
	svc   *webhook.Service
	http  http.Handler
}

func setup(t *testing.T, h webhook.EventHandler) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))
	_, err = db.Pool().Exec(ctx, `CREATE TABLE handler_writes (event_id text NOT NULL)`)
	require.NoError(t, err)

	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	svc, err := webhook.NewService(db.Pool(), db, clk, webhook.Config{Workers: 2, MaxAttempts: 3, PollInterval: tick}, nil, nil)
	require.NoError(t, err)
	if h != nil {
		svc.SetHandler(h)
	}
	mux := http.NewServeMux()
	webhook.NewHandler(svc, secret, clk, nil, nil).Routes(mux)
	return fixture{db: db, clock: clk, svc: svc, http: mux}
}

// start runs the inbox workers until the test ends.
func (f fixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.svc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(waitFor):
			t.Error("inbox did not stop")
		}
	})
}

// bankEvent is a webhook body as the simulator sends it.
func bankEvent(t *testing.T, eventType, status string) []byte {
	t.Helper()
	ev := banksim.Event{
		EventID: uuid.New(), EventType: eventType, BankReference: "BSIM-" + uuid.NewString(),
		IdempotencyKey: uuid.New(), AmountMinor: 15000, Currency: "EGP", Status: status,
		OccurredAt: time.Date(2026, 11, 9, 9, 59, 0, 0, time.UTC),
	}
	if status == webhook.StatusFailed {
		ev.FailureCode, ev.FailureMessage = "INSUFFICIENT_FUNDS", "The account has insufficient funds."
	}
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	return b
}

func (f fixture) post(t *testing.T, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/bank", bytes.NewReader(payload))
	req.Header.Set(webhook.SignatureHeader, banksim.Sign(secret, f.clock.Now(), payload))
	rec := httptest.NewRecorder()
	f.http.ServeHTTP(rec, req)
	return rec
}

type row struct {
	status, eventType, providerEventID string
	attempts                           int
	payload                            []byte
	lastError                          *string
	processedAt                        *time.Time
}

func (f fixture) rows(t *testing.T) []row {
	t.Helper()
	rs, err := f.db.Pool().Query(context.Background(), `SELECT status, event_type, provider_event_id, attempts, payload, last_error, processed_at
		FROM inbox_events WHERE provider = 'banksim' ORDER BY received_at, id`)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rs, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.status, &x.eventType, &x.providerEventID, &x.attempts, &x.payload, &x.lastError, &x.processedAt)
		return x, err
	})
	require.NoError(t, err)
	return out
}

func (f fixture) status(t *testing.T) string {
	t.Helper()
	rs := f.rows(t)
	require.Len(t, rs, 1)
	return rs[0].status
}

func (f fixture) handlerWrites(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM handler_writes`).Scan(&n))
	return n
}

// AC-1.
func TestWebhook_StoreAndProcess(t *testing.T) {
	h := &spyHandler{}
	f := setup(t, h)
	payload := bankEvent(t, webhook.EventDebitSucceeded, webhook.StatusSucceeded)

	rec := f.post(t, payload)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())

	rs := f.rows(t)
	require.Len(t, rs, 1)
	assert.Equal(t, "PENDING", rs[0].status)
	assert.Equal(t, webhook.EventDebitSucceeded, rs[0].eventType)
	assert.JSONEq(t, string(payload), string(rs[0].payload), "the raw payload is kept (NFR-9)")
	var sent banksim.Event
	require.NoError(t, json.Unmarshal(payload, &sent))
	assert.Equal(t, sent.EventID.String(), rs[0].providerEventID)

	f.start(t)
	require.Eventually(t, func() bool { return f.status(t) == "DONE" }, waitFor, tick)
	calls := h.calls()
	require.Len(t, calls, 1)
	got := calls[0]
	assert.Equal(t, sent.EventID.String(), got.EventID)
	assert.Equal(t, webhook.EventDebitSucceeded, got.Type)
	assert.Equal(t, sent.BankReference, got.BankReference)
	assert.Equal(t, sent.IdempotencyKey, got.IdempotencyKey)
	assert.Equal(t, int64(15000), got.Amount.Amount())
	assert.True(t, got.Succeeded())
	assert.True(t, got.OccurredAt.Equal(sent.OccurredAt))
	assert.Equal(t, 1, f.handlerWrites(t), "the handler's write committed")
	rs = f.rows(t)
	assert.Equal(t, 1, rs[0].attempts)
	assert.NotNil(t, rs[0].processedAt)
}

// AC-2: the bank delivers the same event 5 times, some at the same moment.
func TestWebhook_DuplicateDelivery(t *testing.T) {
	h := &spyHandler{}
	f := setup(t, h)
	payload := bankEvent(t, webhook.EventPayoutFailed, webhook.StatusFailed)

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Go(func() { codes[i] = f.post(t, payload).Code })
	}
	wg.Wait()
	assert.Equal(t, []int{200, 200, 200, 200, 200}, codes)
	require.Len(t, f.rows(t), 1)

	f.start(t)
	require.Eventually(t, func() bool { return f.status(t) == "DONE" }, waitFor, tick)
	assert.Equal(t, http.StatusOK, f.post(t, payload).Code, "a late duplicate is acknowledged too")
	time.Sleep(10 * tick) // give the workers a chance to (wrongly) handle it again
	assert.Len(t, h.calls(), 1)
	assert.Len(t, f.rows(t), 1)
}

// AC-3 against the real table: rejected events are not stored.
func TestWebhook_InvalidSignatureNotStored(t *testing.T) {
	f := setup(t, nil)
	payload := bankEvent(t, webhook.EventDebitSucceeded, webhook.StatusSucceeded)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/bank", bytes.NewReader(payload))
	req.Header.Set(webhook.SignatureHeader, banksim.Sign([]byte("another-secret-of-32-bytes-long!"), f.clock.Now(), payload))
	rec := httptest.NewRecorder()
	f.http.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, f.rows(t))
}

// AC-5: no transaction matches; the event is retried with backoff, then DEAD.
func TestInbox_UnknownTransactionThenDead(t *testing.T) {
	h := &spyHandler{errs: []error{
		fmt.Errorf("find: %w", webhook.ErrUnknownTransaction), webhook.ErrUnknownTransaction, webhook.ErrUnknownTransaction,
	}}
	f := setup(t, h)
	require.Equal(t, http.StatusOK, f.post(t, bankEvent(t, webhook.EventDebitSucceeded, webhook.StatusSucceeded)).Code)
	f.start(t)

	require.Eventually(t, func() bool { return len(h.calls()) == 1 }, waitFor, tick)
	require.Eventually(t, func() bool { return f.status(t) == "PENDING" }, waitFor, tick)
	rs := f.rows(t)
	assert.Equal(t, 1, rs[0].attempts)
	require.NotNil(t, rs[0].lastError)
	assert.Contains(t, *rs[0].lastError, "unknown transaction")

	// The retry waits for its backoff: without moving the clock nothing happens.
	time.Sleep(10 * tick)
	assert.Len(t, h.calls(), 1)

	require.Eventually(t, func() bool {
		f.clock.Advance(10 * time.Minute) // past any backoff (capped at 5 minutes)
		return f.status(t) == "DEAD"
	}, waitFor, tick)
	assert.Len(t, h.calls(), 3, "MaxAttempts")
	assert.Zero(t, f.handlerWrites(t), "each failed attempt rolled back")
}

// AC-5: an unknown event type fails at once without calling the handler.
func TestInbox_UnknownTypeFailed(t *testing.T) {
	h := &spyHandler{}
	f := setup(t, h)
	payload := []byte(`{"event_id":"evt-refund-1","event_type":"refund.succeeded","amount_minor":100}`)
	require.Equal(t, http.StatusOK, f.post(t, payload).Code)
	f.start(t)

	require.Eventually(t, func() bool { return f.status(t) == "FAILED" }, waitFor, tick)
	rs := f.rows(t)
	require.NotNil(t, rs[0].lastError)
	assert.Contains(t, *rs[0].lastError, `unknown event_type "refund.succeeded"`)
	assert.Equal(t, 1, rs[0].attempts)
	assert.NotNil(t, rs[0].processedAt)
	assert.Empty(t, h.calls())
}

// AC-6: the handler's writes and its error roll back together, and the event is retried.
func TestInbox_HandlerRollback(t *testing.T) {
	h := &spyHandler{errs: []error{errors.New("ledger is busy")}}
	f := setup(t, h)
	require.Equal(t, http.StatusOK, f.post(t, bankEvent(t, webhook.EventPayoutSucceeded, webhook.StatusSucceeded)).Code)
	f.start(t)

	require.Eventually(t, func() bool { return len(h.calls()) == 1 && f.status(t) == "PENDING" }, waitFor, tick)
	assert.Zero(t, f.handlerWrites(t), "the write of the failed attempt was rolled back")
	rs := f.rows(t)
	require.NotNil(t, rs[0].lastError)
	assert.Contains(t, *rs[0].lastError, "ledger is busy")

	require.Eventually(t, func() bool {
		f.clock.Advance(10 * time.Minute)
		return f.status(t) == "DONE"
	}, waitFor, tick)
	assert.Len(t, h.calls(), 2)
	assert.Equal(t, 1, f.handlerWrites(t), "only the successful attempt's write is kept")
}

// AC-8: without workers (INBOX_ENABLED=false, or no handler yet) events are stored and wait.
func TestInbox_NotRunning_EventsWait(t *testing.T) {
	f := setup(t, nil)
	require.Equal(t, http.StatusOK, f.post(t, bankEvent(t, webhook.EventDebitFailed, webhook.StatusFailed)).Code)
	time.Sleep(10 * tick)
	assert.Equal(t, "PENDING", f.status(t))
}

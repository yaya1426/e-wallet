package banksim

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// Event is the webhook body (TDR-007 §4).
type Event struct {
	EventID        uuid.UUID `json:"event_id"`
	EventType      string    `json:"event_type"` // debit.succeeded | debit.failed | payout.succeeded | payout.failed
	BankReference  string    `json:"bank_reference"`
	IdempotencyKey uuid.UUID `json:"idempotency_key"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	FailureCode    string    `json:"failure_code,omitempty"`
	FailureMessage string    `json:"failure_message,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// notifier settles operations after a delay and delivers their webhook, retrying on failure.
// Every goroutine stops when ctx ends; pending webhooks are then dropped (acceptable for a
// simulator, TDR-008 §2).
type notifier struct {
	ctx     context.Context
	url     string
	secret  []byte
	retries []time.Duration
	client  *http.Client
	clock   clock.Clock
	log     *slog.Logger
	wg      sync.WaitGroup
}

func newNotifier(ctx context.Context, cfg Config, clk clock.Clock, log *slog.Logger) *notifier {
	return &notifier{
		ctx: ctx, url: cfg.WebhookURL, secret: cfg.WebhookSecret, retries: cfg.RetryDelays,
		client: &http.Client{Timeout: cfg.WebhookTimeout}, clock: clk, log: log,
	}
}

// settle waits delay, then calls finish (which records the final status, so GET
// /v1/operations shows it before the first webhook attempt) and delivers its event.
func (n *notifier) settle(key uuid.UUID, delay time.Duration, finish func() operation) {
	n.wg.Go(func() {
		if !n.sleep(delay) {
			return
		}
		op := finish()
		n.deliver(Event{
			EventID: id.New(), EventType: strings.ToLower(op.Kind) + "." + strings.ToLower(op.Status),
			BankReference: op.BankReference, IdempotencyKey: key, AmountMinor: op.AmountMinor, Currency: op.Currency,
			Status: op.Status, FailureCode: op.FailureCode, FailureMessage: op.FailureMessage,
			OccurredAt: n.clock.Now().UTC(),
		})
	})
}

// deliver sends ev, then retries after each of n.retries until a 2xx. Each attempt is signed
// with its own timestamp; the event (and its event_id) stays the same.
func (n *notifier) deliver(ev Event) {
	body := mustJSON(ev)
	log := n.log.With(slog.String("event_id", ev.EventID.String()), slog.String("event_type", ev.EventType),
		slog.String("idempotency_key", ev.IdempotencyKey.String()))
	for attempt := 1; ; attempt++ {
		err := n.post(body)
		if err == nil {
			log.Info("banksim webhook delivered", slog.Int("attempt", attempt))
			return
		}
		if attempt > len(n.retries) {
			log.Error("banksim webhook given up", slog.Int("attempts", attempt), slog.Any("error", err))
			return
		}
		log.Warn("banksim webhook failed; will retry", slog.Int("attempt", attempt), slog.Any("error", err))
		if !n.sleep(n.retries[attempt-1]) {
			return
		}
	}
}

func (n *notifier) post(body []byte) error {
	req, err := http.NewRequestWithContext(n.ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, Sign(n.secret, n.clock.Now(), body))
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRequestBytes))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// sleep waits d; it returns false when ctx ended first.
func (n *notifier) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-n.ctx.Done():
		return false
	}
}

func (n *notifier) wait() { n.wg.Wait() }

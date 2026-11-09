// Package banksim is the bank.Gateway adapter for the bank simulator's HTTP API
// (api/openapi/banksim.yaml, TDR-008). It maps every answer to an Acceptance, an Operation,
// a *bank.RejectedError, bank.ErrTemporary or bank.ErrNotFound.
package banksim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// maxBodyBytes bounds how much of a response body is read.
const maxBodyBytes = 64 << 10

// Operation names in logs and the bank_call_duration_seconds metric.
const (
	OpCreateDebit  = "create_debit"
	OpCreatePayout = "create_payout"
	OpGetOperation = "get_operation"
)

// Results in the bank_call_duration_seconds metric.
const (
	ResultOK        = "ok"
	ResultRejected  = "rejected"
	ResultTemporary = "temporary"
	ResultNotFound  = "not_found"
)

// Observer receives the duration of every bank call (TDR-006 §6).
type Observer interface {
	ObserveBankCall(op, result string, duration time.Duration)
}

// Config configures the client.
type Config struct {
	BaseURL string        // BANK_BASE_URL, e.g. http://banksim:9090
	Timeout time.Duration // BANK_HTTP_TIMEOUT, per call
}

// Client implements bank.Gateway over HTTP.
type Client struct {
	base *url.URL
	http *http.Client
	log  *slog.Logger
	obs  Observer
}

var _ bank.Gateway = (*Client)(nil)

// New returns a client. log and obs may be nil.
func New(cfg Config, log *slog.Logger, obs Observer) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("banksim: base URL must be an absolute http(s) URL, got %q", cfg.BaseURL)
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("banksim: timeout must be positive, got %s", cfg.Timeout)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if obs == nil {
		obs = noopObserver{}
	}
	return &Client{
		base: base,
		http: &http.Client{
			Timeout: cfg.Timeout,
			// A redirect is not part of the contract; it is answered as a temporary failure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		log: log,
		obs: obs,
	}, nil
}

// operationRequest is the body of POST /v1/debits and /v1/payouts.
type operationRequest struct {
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	IBAN        string    `json:"iban"`
	HolderName  string    `json:"holder_name"`
	Reference   uuid.UUID `json:"reference"`
}

type acceptanceBody struct {
	BankReference string `json:"bank_reference"`
	Status        string `json:"status"`
}

type operationBody struct {
	IdempotencyKey uuid.UUID `json:"idempotency_key"`
	BankReference  string    `json:"bank_reference"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// CreateDebit sends POST /v1/debits.
func (c *Client) CreateDebit(ctx context.Context, req bank.DebitRequest) (bank.Acceptance, error) {
	return c.create(ctx, OpCreateDebit, "/v1/debits", req.IdempotencyKey, operationRequest{
		AmountMinor: req.Amount.Amount(), Currency: string(req.Amount.Currency()),
		IBAN: req.IBAN, HolderName: req.HolderName, Reference: req.IdempotencyKey,
	})
}

// CreatePayout sends POST /v1/payouts.
func (c *Client) CreatePayout(ctx context.Context, req bank.PayoutRequest) (bank.Acceptance, error) {
	return c.create(ctx, OpCreatePayout, "/v1/payouts", req.IdempotencyKey, operationRequest{
		AmountMinor: req.Amount.Amount(), Currency: string(req.Amount.Currency()),
		IBAN: req.IBAN, HolderName: req.HolderName, Reference: req.IdempotencyKey,
	})
}

func (c *Client) create(ctx context.Context, op, path string, key uuid.UUID, body operationRequest) (acc bank.Acceptance, err error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return bank.Acceptance{}, fmt.Errorf("banksim: encode %s: %w", op, err)
	}
	defer c.observe(ctx, op, key, time.Now(), &err)

	status, raw, err := c.do(ctx, http.MethodPost, path, key, payload)
	if err != nil {
		return bank.Acceptance{}, err
	}
	if status != http.StatusAccepted {
		return bank.Acceptance{}, classify(status, raw)
	}
	var a acceptanceBody
	if err := json.Unmarshal(raw, &a); err != nil || a.BankReference == "" {
		return bank.Acceptance{}, fmt.Errorf("%w: %s: unreadable 202 body", bank.ErrTemporary, op)
	}
	return bank.Acceptance{BankReference: a.BankReference}, nil
}

// GetOperation sends GET /v1/operations/{idempotency_key}.
func (c *Client) GetOperation(ctx context.Context, key uuid.UUID) (operation bank.Operation, err error) {
	defer c.observe(ctx, OpGetOperation, key, time.Now(), &err)

	status, raw, err := c.do(ctx, http.MethodGet, "/v1/operations/"+key.String(), uuid.Nil, nil)
	if err != nil {
		return bank.Operation{}, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return bank.Operation{}, bank.ErrNotFound
	default:
		return bank.Operation{}, classify(status, raw)
	}
	var b operationBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return bank.Operation{}, fmt.Errorf("%w: %s: unreadable 200 body", bank.ErrTemporary, OpGetOperation)
	}
	return b.toOperation()
}

// toOperation validates the body: an unexpected value means the answer cannot be trusted, so
// it is a temporary failure (it should never happen).
func (b operationBody) toOperation() (bank.Operation, error) {
	amount, err := money.New(b.AmountMinor, money.Currency(b.Currency))
	if err != nil {
		return bank.Operation{}, fmt.Errorf("%w: %s: %w", bank.ErrTemporary, OpGetOperation, err)
	}
	kind, status := bank.Kind(b.Kind), bank.OperationStatus(b.Status)
	var problems []string
	if b.IdempotencyKey == uuid.Nil {
		problems = append(problems, "missing idempotency_key")
	}
	if b.BankReference == "" {
		problems = append(problems, "missing bank_reference")
	}
	if kind != bank.KindDebit && kind != bank.KindPayout {
		problems = append(problems, fmt.Sprintf("unknown kind %q", b.Kind))
	}
	if status != bank.StatusPending && status != bank.StatusSucceeded && status != bank.StatusFailed {
		problems = append(problems, fmt.Sprintf("unknown status %q", b.Status))
	}
	if len(problems) > 0 {
		return bank.Operation{}, fmt.Errorf("%w: %s: %s", bank.ErrTemporary, OpGetOperation, strings.Join(problems, ", "))
	}
	return bank.Operation{
		IdempotencyKey: b.IdempotencyKey, BankReference: b.BankReference, Kind: kind, Status: status,
		Amount: amount, FailureCode: b.FailureCode, FailureMessage: b.FailureMessage,
	}, nil
}

// do sends one request and returns the status and the (size-limited) body. Transport
// failures, timeouts and oversized bodies are ErrTemporary. The body is always closed.
func (c *Client) do(ctx context.Context, method, path string, key uuid.UUID, payload []byte) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return 0, nil, fmt.Errorf("banksim: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != uuid.Nil {
		req.Header.Set("Idempotency-Key", key.String())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s %s: %w", bank.ErrTemporary, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s %s: read body: %w", bank.ErrTemporary, method, path, err)
	}
	if len(raw) > maxBodyBytes {
		return 0, nil, fmt.Errorf("%w: %s %s: response body over %d bytes", bank.ErrTemporary, method, path, maxBodyBytes)
	}
	return resp.StatusCode, raw, nil
}

// classify maps a status that is not the expected success to an error (task T013 table).
func classify(status int, raw []byte) error {
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500:
		return fmt.Errorf("%w: HTTP %d", bank.ErrTemporary, status)
	case status >= 400:
		var e errorBody
		if err := json.Unmarshal(raw, &e); err == nil && e.Error.Code != "" {
			return &bank.RejectedError{Code: e.Error.Code, Message: e.Error.Message}
		}
		return &bank.RejectedError{Code: "BANK_REJECTED", Message: http.StatusText(status)}
	default: // 1xx, an unexpected 2xx, 3xx
		return fmt.Errorf("%w: unexpected HTTP %d", bank.ErrTemporary, status)
	}
}

// observe logs the call and records its duration and result.
func (c *Client) observe(ctx context.Context, op string, key uuid.UUID, start time.Time, errp *error) {
	duration := time.Since(start)
	result, level := ResultOK, slog.LevelInfo
	var rejected *bank.RejectedError
	switch err := *errp; {
	case err == nil:
	case errors.As(err, &rejected):
		result = ResultRejected
	case errors.Is(err, bank.ErrNotFound):
		result = ResultNotFound
	default:
		result, level = ResultTemporary, slog.LevelWarn
	}
	c.obs.ObserveBankCall(op, result, duration)
	attrs := []slog.Attr{
		slog.String("bank_op", op), slog.String("idempotency_key", key.String()),
		slog.String("status", result), slog.Duration("duration", duration),
	}
	if *errp != nil {
		attrs = append(attrs, slog.Any("error", *errp))
	}
	c.log.LogAttrs(ctx, level, "bank call", attrs...)
}

type noopObserver struct{}

func (noopObserver) ObserveBankCall(string, string, time.Duration) {}

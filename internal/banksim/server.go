// Package banksim is the bank simulator (TDR-008 §2): an in-memory fake bank that accepts
// debits and payouts idempotently, answers status queries and, after a delay, calls the
// wallet's webhook with an HMAC-signed event (TDR-007). It acts as an external system: it
// shares only the HTTP contract (api/openapi/banksim.yaml) with the wallet, never its code.
package banksim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

const (
	kindDebit  = "DEBIT"
	kindPayout = "PAYOUT"

	statusPending   = "PENDING"
	statusSucceeded = "SUCCEEDED"
	statusFailed    = "FAILED"

	maxRequestBytes = 64 << 10
)

// Error codes of the simulator's API.
const (
	CodeInvalidRequest       = "INVALID_REQUEST"
	CodeIdempotencyKeyReused = "IDEMPOTENCY_KEY_REUSED"
	CodeInvalidAccount       = "INVALID_ACCOUNT"
	CodeNotFound             = "NOT_FOUND"
)

// Config configures the simulator.
type Config struct {
	WebhookURL    string        // BANKSIM_WEBHOOK_URL
	WebhookSecret []byte        // BANK_WEBHOOK_SECRET
	Delay         time.Duration // BANKSIM_WEBHOOK_DELAY: time until an operation settles
	// RetryDelays are the waits before each webhook retry; nil means 1, 2, 4, 8, 16 s.
	RetryDelays []time.Duration
	// WebhookTimeout bounds one webhook call; 0 means 5 s.
	WebhookTimeout time.Duration
}

// DefaultRetryDelays are the waits before the webhook retries (task T014).
var DefaultRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

// Server is the simulator. Background work (settling operations, webhooks) runs on ctx from
// New; Wait blocks until it has stopped after ctx ends.
type Server struct {
	store    *store
	notifier *notifier
	clock    clock.Clock
	log      *slog.Logger
	delay    time.Duration
}

// New returns a simulator. Its background goroutines stop when ctx ends. log may be nil.
func New(ctx context.Context, cfg Config, clk clock.Clock, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.RetryDelays == nil {
		cfg.RetryDelays = DefaultRetryDelays
	}
	if cfg.WebhookTimeout == 0 {
		cfg.WebhookTimeout = 5 * time.Second
	}
	return &Server{
		store:    newStore(),
		notifier: newNotifier(ctx, cfg, clk, log),
		clock:    clk,
		log:      log,
		delay:    cfg.Delay,
	}
}

// Routes mounts the simulator's endpoints.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/debits", func(w http.ResponseWriter, r *http.Request) { s.create(w, r, kindDebit) })
	mux.HandleFunc("POST /v1/payouts", func(w http.ResponseWriter, r *http.Request) { s.create(w, r, kindPayout) })
	mux.HandleFunc("GET /v1/operations/{idempotency_key}", s.getOperation)
	mux.HandleFunc("POST /admin/scenarios", s.setScenario)
}

// Wait blocks until the background goroutines have stopped (after ctx from New ended).
func (s *Server) Wait() { s.notifier.wait() }

// request is the body of POST /v1/debits and /v1/payouts, plus the kind and the key: two
// requests are "the same" when every field is equal.
type request struct {
	Kind        string    `json:"-"`
	Key         uuid.UUID `json:"-"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	IBAN        string    `json:"iban"`
	HolderName  string    `json:"holder_name"`
	Reference   uuid.UUID `json:"reference"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, kind string) {
	req, ok := decodeCreate(w, r)
	if !ok {
		return
	}
	req.Kind = kind

	s.store.mu.Lock()
	if prev, seen := s.store.answers[req.Key]; seen {
		s.store.mu.Unlock()
		if prev.fingerprint != req {
			writeError(w, http.StatusUnprocessableEntity, CodeIdempotencyKeyReused, "The Idempotency-Key was used with a different request.")
			return
		}
		writeRaw(w, prev.status, prev.body)
		return
	}
	var (
		status int
		body   []byte
		op     *operation
	)
	switch scenario := s.store.pickScenario(req.IBAN); scenario {
	case ScenarioInvalidAccount:
		status, body = http.StatusUnprocessableEntity, errorJSON(CodeInvalidAccount, "The bank account is not valid.")
	default:
		op = &operation{
			Key: req.Key, BankReference: "BSIM-" + id.New().String(), Kind: kind, Status: statusPending,
			AmountMinor: req.AmountMinor, Currency: req.Currency,
		}
		s.store.ops[req.Key] = op
		status, body = http.StatusAccepted, mustJSON(map[string]string{"bank_reference": op.BankReference, "status": statusPending})
		s.notifier.settle(req.Key, s.delay, func() operation {
			if scenario == ScenarioFailLater {
				code, msg := failure(kind)
				return s.store.finish(req.Key, statusFailed, code, msg)
			}
			return s.store.finish(req.Key, statusSucceeded, "", "")
		})
	}
	s.store.answers[req.Key] = answer{fingerprint: req, status: status, body: body}
	s.store.mu.Unlock()

	attrs := []any{slog.String("kind", kind), slog.String("idempotency_key", req.Key.String()), slog.Int("status", status)}
	if op != nil {
		attrs = append(attrs, slog.String("bank_reference", op.BankReference))
	}
	s.log.InfoContext(r.Context(), "banksim operation requested", attrs...)
	writeRaw(w, status, body)
}

// decodeCreate reads and validates a create request; on failure it writes 400.
func decodeCreate(w http.ResponseWriter, r *http.Request) (request, bool) {
	invalid := func(msg string) (request, bool) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg)
		return request{}, false
	}
	key, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil {
		return invalid("The Idempotency-Key header must be a UUID.")
	}
	var req request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return invalid("The body must be a JSON object with the documented fields.")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalid("The body must contain one JSON object.")
	}
	req.Key = key
	switch {
	case req.AmountMinor <= 0:
		return invalid("amount_minor must be positive.")
	case req.Currency != "EGP":
		return invalid("currency must be EGP.")
	case strings.TrimSpace(req.IBAN) == "":
		return invalid("iban is required.")
	case strings.TrimSpace(req.HolderName) == "":
		return invalid("holder_name is required.")
	case req.Reference != key:
		return invalid("reference must equal the Idempotency-Key.")
	}
	return req, true
}

type operationResponse struct {
	IdempotencyKey uuid.UUID `json:"idempotency_key"`
	BankReference  string    `json:"bank_reference"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	FailureCode    string    `json:"failure_code,omitempty"`
	FailureMessage string    `json:"failure_message,omitempty"`
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	key, err := uuid.Parse(r.PathValue("idempotency_key"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "The idempotency key must be a UUID.")
		return
	}
	op, ok := s.store.operation(key)
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "No operation was created with this key.")
		return
	}
	writeRaw(w, http.StatusOK, mustJSON(operationResponse{
		IdempotencyKey: op.Key, BankReference: op.BankReference, Kind: op.Kind, Status: op.Status,
		AmountMinor: op.AmountMinor, Currency: op.Currency, FailureCode: op.FailureCode, FailureMessage: op.FailureMessage,
	}))
}

// setScenario is POST /admin/scenarios: the next debit or payout for the IBAN uses the
// scenario, once (test only).
func (s *Server) setScenario(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IBAN     string `json:"iban"`
		Scenario string `json:"scenario"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || strings.TrimSpace(body.IBAN) == "" || !validScenario(body.Scenario) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, `Send {"iban": "...", "scenario": "0000" | "0001" | "0002"}.`)
		return
	}
	s.store.setOverride(body.IBAN, body.Scenario)
	s.log.InfoContext(r.Context(), "banksim scenario set", slog.String("scenario", body.Scenario))
	w.WriteHeader(http.StatusNoContent)
}

func errorJSON(code, message string) []byte {
	type detail struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	return mustJSON(struct {
		Error detail `json:"error"`
	}{detail{code, message}})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeRaw(w, status, errorJSON(code, message))
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body) // the status is sent; a failed write means the client left
}

// mustJSON encodes values whose encoding cannot fail (plain structs and maps of strings).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

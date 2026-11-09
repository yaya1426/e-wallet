package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// CodeInvalidSignature answers a webhook whose signature does not verify.
const CodeInvalidSignature = "INVALID_SIGNATURE"

// maxEventIDLength bounds provider_event_id; the simulator sends a UUID (36 characters).
const maxEventIDLength = 200

// Handler serves POST /webhooks/bank.
type Handler struct {
	svc    *Service
	secret []byte
	clock  clock.Clock
	log    *slog.Logger
	obs    Observer
}

// NewHandler returns the webhook endpoint. secret is the HMAC key shared with the bank
// (BANK_WEBHOOK_SECRET). log and obs may be nil.
func NewHandler(svc *Service, secret []byte, clk clock.Clock, log *slog.Logger, obs Observer) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if obs == nil {
		obs = noopObserver{}
	}
	return &Handler{svc: svc, secret: secret, clock: clk, log: log, obs: obs}
}

// Routes mounts the endpoint. It has no user authentication: the signature authenticates
// the bank.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/bank", h.receive)
}

// envelope is the part of the body the endpoint reads; the workers decode the rest.
type envelope struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
}

// receive verifies, stores and acknowledges one event (TDR-007 §1). No business logic runs
// here; a duplicate event_id is acknowledged like a new one.
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpserver.MaxBodyBytes))
	if err != nil {
		h.obs.ObserveWebhookReceived(ReceivedMalformed)
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, httpserver.CodeRequestTooLarge, "Request body is larger than 64 KB.")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeMalformedRequest, "The request body could not be read.")
		return
	}
	// The signature covers the raw bytes, so it is checked before any parsing.
	if err := Verify(h.secret, r.Header.Get(SignatureHeader), body, h.clock.Now()); err != nil {
		h.obs.ObserveWebhookReceived(ReceivedInvalidSignature)
		h.log.WarnContext(r.Context(), "bank webhook rejected", slog.String("reason", "invalid signature"))
		httpserver.WriteError(w, http.StatusUnauthorized, CodeInvalidSignature, "The webhook signature is invalid.")
		return
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil || env.EventID == "" || env.EventType == "" || len(env.EventID) > maxEventIDLength {
		h.obs.ObserveWebhookReceived(ReceivedMalformed)
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeMalformedRequest,
			"The body must be a JSON object with event_id and event_type.")
		return
	}
	log := h.log.With(slog.String("event_id", env.EventID), slog.String("event_type", env.EventType))
	stored, err := h.svc.Store(r.Context(), env.EventID, env.EventType, body)
	if err != nil {
		h.obs.ObserveWebhookReceived(ReceivedError)
		log.ErrorContext(r.Context(), "bank webhook not stored", slog.Any("error", err))
		// Not stored: a 5xx makes the bank retry (TDR-007 §1).
		httpserver.WriteError(w, http.StatusServiceUnavailable, httpserver.CodeTryAgain, "The event could not be stored. Please retry.")
		return
	}
	result := ReceivedDuplicate
	if stored {
		result = ReceivedStored
	}
	h.obs.ObserveWebhookReceived(result)
	log.InfoContext(r.Context(), "bank webhook received", slog.String("result", result))
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

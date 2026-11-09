package idempotency

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// Headers (TDR-005 layer 1).
const (
	HeaderKey      = "Idempotency-Key"
	HeaderReplayed = "Idempotent-Replayed"
)

// maxKeyLength is the longest accepted key (TDR-005).
const maxKeyLength = 64

// Error codes (T007, catalogued in api/openapi/wallet.yaml by the endpoints that use them).
const (
	CodeKeyRequired       = "IDEMPOTENCY_KEY_REQUIRED"
	CodeKeyInvalid        = "IDEMPOTENCY_KEY_INVALID"
	CodeKeyReused         = "IDEMPOTENCY_KEY_REUSED"
	CodeRequestInProgress = "REQUEST_IN_PROGRESS"
)

// KeyFromRequest returns the Idempotency-Key header: ErrKeyRequired when missing or empty,
// ErrKeyInvalid when longer than 64 characters or not a UUID (any version).
func KeyFromRequest(r *http.Request) (string, error) {
	key := r.Header.Get(HeaderKey)
	switch {
	case key == "":
		return "", ErrKeyRequired
	case len(key) > maxKeyLength:
		return "", ErrKeyInvalid
	}
	if _, err := uuid.Parse(key); err != nil {
		return "", ErrKeyInvalid
	}
	return key, nil
}

// WriteReplay writes a stored response as it was first sent, marked as replayed.
func WriteReplay(w http.ResponseWriter, replay Replay) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(HeaderReplayed, "true")
	w.WriteHeader(replay.Status)
	_, _ = w.Write(replay.Body) // the status is already sent; a failed write means the client left
}

// WriteError writes the API answer for an idempotency error and reports whether err was one.
func WriteError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrKeyRequired):
		httpserver.WriteError(w, http.StatusBadRequest, CodeKeyRequired, "The Idempotency-Key header is required.")
	case errors.Is(err, ErrKeyInvalid):
		httpserver.WriteError(w, http.StatusBadRequest, CodeKeyInvalid, "The Idempotency-Key header must be a UUID of at most 64 characters.")
	case errors.Is(err, ErrKeyReused):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, CodeKeyReused, "This Idempotency-Key was already used for a different request.")
	case errors.Is(err, ErrRequestInProgress):
		httpserver.WriteError(w, http.StatusConflict, CodeRequestInProgress, "A request with this Idempotency-Key is still in progress. Try again shortly.")
	default:
		return false
	}
	return true
}

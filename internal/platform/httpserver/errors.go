package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yaya1426/e-wallet/internal/platform/database"
)

// Error codes shared by every HTTP surface (TDR-013).
const (
	CodeNotFound         = "NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeInternal         = "INTERNAL_ERROR"
	// CodeTryAgain answers database.ErrTryAgain: the request lost a conflict with others
	// and may be retried as is (TDR-010 §5).
	CodeTryAgain = "TRY_AGAIN"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError writes the TDR-013 error body. The request ID is taken from the
// X-Request-ID response header set by the request ID middleware.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody(w, code, message))
}

// ErrorBody returns the TDR-013 error body WriteError would write, for handlers that must
// store it before writing it (idempotent replays).
func ErrorBody(w http.ResponseWriter, code, message string) any {
	return errorBody{Error: errorDetail{Code: code, Message: message, RequestID: w.Header().Get(HeaderRequestID)}}
}

// WriteTryAgain writes 503 TRY_AGAIN when err is database.ErrTryAgain (TDR-010 §5) and
// reports whether it did.
func WriteTryAgain(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, database.ErrTryAgain) {
		return false
	}
	WriteError(w, http.StatusServiceUnavailable, CodeTryAgain, "The request conflicted with another one. Please try again.")
	return true
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the status is already sent; a failed write means the client left
}

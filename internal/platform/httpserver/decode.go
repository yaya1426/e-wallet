package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxBodyBytes is the largest request body the API accepts (TDR-013).
const MaxBodyBytes = 64 << 10

// Error codes for request bodies (TDR-013, TDR-017).
const (
	CodeMalformedRequest = "MALFORMED_REQUEST"
	CodeRequestTooLarge  = "REQUEST_TOO_LARGE"
)

// DecodeError is a request body the API rejects. Write it with WriteDecodeError.
type DecodeError struct {
	Status  int
	Code    string
	Message string
	cause   error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("httpserver: decode body: %s: %v", e.Code, e.cause)
}

func (e *DecodeError) Unwrap() error { return e.cause }

// DecodeJSON reads exactly one JSON object from the body into dst (TDR-013): at most
// MaxBodyBytes, the value must be an object (not null, an array or a scalar), unknown fields
// are rejected, and nothing may follow it. Any problem is a *DecodeError: 413
// REQUEST_TOO_LARGE for an oversized body, otherwise 400 MALFORMED_REQUEST.
//
// Like encoding/json in general, keys match struct tags case-insensitively and a repeated
// key keeps its last value.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		return newDecodeError(err)
	}
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return newDecodeError(errors.New("body is not a JSON object"))
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return newDecodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return newDecodeError(err)
	}
	return nil
}

// WriteDecodeError writes the error body for a DecodeJSON error.
func WriteDecodeError(w http.ResponseWriter, err error) {
	var de *DecodeError
	if !errors.As(err, &de) {
		de = &DecodeError{Status: http.StatusBadRequest, Code: CodeMalformedRequest, Message: "Malformed request body."}
	}
	WriteError(w, de.Status, de.Code, de.Message)
}

func newDecodeError(err error) *DecodeError {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &DecodeError{Status: http.StatusRequestEntityTooLarge, Code: CodeRequestTooLarge,
			Message: "Request body is larger than 64 KB.", cause: err}
	}
	return &DecodeError{Status: http.StatusBadRequest, Code: CodeMalformedRequest,
		Message: "Malformed request body.", cause: err}
}

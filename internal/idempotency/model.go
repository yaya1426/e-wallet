// Package idempotency makes money POSTs safe to repeat (TDR-005 layer 1, PRD-001 BR-16..18).
// Handlers call Begin and Complete inside their own unit of work, so the key row commits or
// rolls back together with the business change.
package idempotency

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// Request identifies one idempotent request.
type Request struct {
	UserID uuid.UUID
	Key    string
	Method string
	Path   string
	Hash   []byte // RequestHash of the decoded request
}

// Replay is the stored response of a completed request with the same key.
type Replay struct {
	Status int
	Body   json.RawMessage
}

var (
	// ErrKeyRequired means the Idempotency-Key header is missing or empty.
	ErrKeyRequired = errors.New("idempotency: key required")
	// ErrKeyInvalid means the key is not a UUID or is longer than 64 characters.
	ErrKeyInvalid = errors.New("idempotency: key invalid")
	// ErrKeyReused means the key was used for a different request (hash, method or path).
	ErrKeyReused = errors.New("idempotency: key reused for a different request")
	// ErrRequestInProgress means another request with the same key is still running. It is a
	// plain sentinel on purpose: database.InTx does not retry it (TDR-017 §2).
	ErrRequestInProgress = errors.New("idempotency: request in progress")
	// ErrNotBegun means Complete found no IN_PROGRESS key: Begin was not called in this
	// transaction, or the key was already completed.
	ErrNotBegun = errors.New("idempotency: key not begun")
)

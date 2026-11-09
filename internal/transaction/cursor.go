package transaction

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCursor means a history cursor could not be decoded.
var ErrInvalidCursor = errors.New("transaction: invalid cursor")

// Cursor is a position in a wallet's history: the next page starts strictly after the row
// with this (created_at, id), newest first (TDR-013 keyset pagination).
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type cursorJSON struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

// Encode returns the opaque cursor: base64url (no padding) of {"t": RFC 3339 nano, "id": uuid}.
func (c Cursor) Encode() string {
	raw, _ := json.Marshal(cursorJSON{T: c.CreatedAt.UTC().Format(time.RFC3339Nano), ID: c.ID.String()}) // cannot fail
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an opaque cursor from Encode.
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var c cursorJSON
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.T)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	id, err := uuid.Parse(c.ID)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{CreatedAt: t, ID: id}, nil
}

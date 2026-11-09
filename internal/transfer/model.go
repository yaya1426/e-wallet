// Package transfer is the P2P use case (PRD-004): it moves money between two wallets in one
// DB transaction, orchestrating identity, wallet, ledger, transaction and idempotency.
package transfer

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

// maxNoteRunes is the longest note (PRD-004 P2P-4).
const maxNoteRunes = 140

// Input is a validated transfer request.
type Input struct {
	SenderID       uuid.UUID
	RecipientPhone string
	Amount         money.Money
	Note           string // trimmed; "" means none
	Idempotency    idempotency.Request
}

// Result is a completed transfer.
type Result struct {
	Transaction         transaction.Transaction
	RecipientPhone      string
	RecipientMaskedName string
	SenderBalanceAfter  int64 // piasters
}

// Errors found before the transfer starts (checks 4–7 of the T008 table); not stored for
// idempotency.
var (
	ErrInvalidAmount       = errors.New("transfer: amount must be a positive integer")
	ErrUnsupportedCurrency = errors.New("transfer: unsupported currency")
	ErrInvalidPhone        = errors.New("transfer: invalid recipient phone")
	ErrInvalidNote         = errors.New("transfer: note longer than 140 characters")
)

// Business errors found inside the unit of work (checks 9–10); stored and replayed for the
// same idempotency key, like the wallet limit errors.
var (
	ErrRecipientNotFound = errors.New("transfer: recipient not found")
	ErrSelfTransfer      = errors.New("transfer: cannot send to yourself")
)

// NormalizeNote trims the note and checks its length.
func NormalizeNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNoteRunes {
		return "", ErrInvalidNote
	}
	return note, nil
}

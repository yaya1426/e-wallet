// Package transaction owns the business record of every user action (PRD-001 BR-11) and its
// state machine (architecture §5.5). The ledger records the money; this module records what
// the user asked for and where it stands (TDR-004 §1).
package transaction

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Type is what the user asked for.
type Type string

// Transaction types.
const (
	TypeTopUp      Type = "TOPUP"
	TypeWithdrawal Type = "WITHDRAWAL"
	TypeP2P        Type = "P2P"
)

// Status is where a transaction stands (PRD-001 BR-14).
type Status string

// Transaction statuses. COMPLETED and FAILED are terminal (BR-15).
const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// Terminal reports whether no transition can leave the status.
func (s Status) Terminal() bool { return s == StatusCompleted || s == StatusFailed }

// transitions is the state machine (architecture §5.5).
var transitions = map[Status][]Status{
	StatusPending:    {StatusProcessing, StatusFailed},
	StatusProcessing: {StatusCompleted, StatusFailed},
}

// CanTransition reports whether from → to is an allowed transition.
func CanTransition(from, to Status) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// maxNoteRunes is the longest P2P note (PRD-004 P2P-4).
const maxNoteRunes = 140

// BankAccountSnapshot is the bank account a top-up or withdrawal was requested for, kept on
// the transaction so later changes to the linked account do not affect it (T005).
type BankAccountSnapshot struct {
	IBAN       string `json:"iban"`
	HolderName string `json:"holder_name"`
}

// Transaction is a user action and its state.
type Transaction struct {
	ID                  uuid.UUID // also the bank idempotency key (TDR-013)
	Type                Type
	Status              Status
	InitiatorUserID     uuid.UUID
	SourceWalletID      uuid.UUID // uuid.Nil when the type has none (top-up)
	DestinationWalletID uuid.UUID // uuid.Nil when the type has none (withdrawal)
	Amount              money.Money
	IdempotencyKey      string
	BankReference       string // "" until the bank accepts the operation
	BankAccount         *BankAccountSnapshot
	Note                string
	FailureCode         string
	FailureMessage      string
	LastReconciledAt    *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time // set when the status becomes terminal
}

// New describes a transaction to create.
type New struct {
	Type                Type
	Status              Status // PENDING for top-up and withdrawal, COMPLETED for P2P
	InitiatorUserID     uuid.UUID
	SourceWalletID      uuid.UUID
	DestinationWalletID uuid.UUID
	Amount              money.Money
	IdempotencyKey      string
	BankAccount         *BankAccountSnapshot
	Note                string
}

// Patch carries the optional fields a transition may set. A nil field keeps its value.
type Patch struct {
	BankReference  *string
	FailureCode    *string
	FailureMessage *string
}

var (
	// ErrInvalidInitialStatus means the status given to Create does not fit the type.
	ErrInvalidInitialStatus = errors.New("transaction: invalid initial status")
	// ErrInvalidWallets means the source/destination wallets do not fit the type.
	ErrInvalidWallets = errors.New("transaction: invalid wallets for type")
	// ErrInvalidTransaction means another field of New is invalid.
	ErrInvalidTransaction = errors.New("transaction: invalid transaction")
	// ErrInvalidTransition means from → to is not in the state machine.
	ErrInvalidTransition = errors.New("transaction: invalid transition")
	// ErrStatusConflict means the row was not in the expected status. Match it with
	// errors.Is; errors.As with *StatusConflictError gives the current status.
	ErrStatusConflict = errors.New("transaction: status conflict")
	// ErrNotFound means no transaction has the ID or bank reference.
	ErrNotFound = errors.New("transaction: not found")
	// ErrDuplicateIdempotencyKey means the user already has a transaction with this key
	// (TDR-005 layer 2).
	ErrDuplicateIdempotencyKey = errors.New("transaction: duplicate idempotency key")
	// ErrDuplicateBankReference means another transaction already has this bank reference.
	ErrDuplicateBankReference = errors.New("transaction: duplicate bank reference")
)

// StatusConflictError is ErrStatusConflict with the status the row actually has.
type StatusConflictError struct {
	Expected Status
	Current  Status
}

func (e *StatusConflictError) Error() string {
	return fmt.Sprintf("transaction: status conflict: expected %s, current %s", e.Expected, e.Current)
}

// Is makes errors.Is(err, ErrStatusConflict) true.
func (e *StatusConflictError) Is(target error) bool { return target == ErrStatusConflict }

// validate checks New against the type rules (T006 AC-2).
func (n New) validate() error {
	switch n.Type {
	case TypeTopUp, TypeWithdrawal:
		if n.Status != StatusPending {
			return fmt.Errorf("%w: %s must start PENDING, got %q", ErrInvalidInitialStatus, n.Type, n.Status)
		}
	case TypeP2P:
		if n.Status != StatusCompleted {
			return fmt.Errorf("%w: P2P must start COMPLETED, got %q", ErrInvalidInitialStatus, n.Status)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidTransaction, n.Type)
	}

	hasSource, hasDestination := n.SourceWalletID != uuid.Nil, n.DestinationWalletID != uuid.Nil
	switch {
	case n.Type == TypeTopUp && (hasSource || !hasDestination),
		n.Type == TypeWithdrawal && (!hasSource || hasDestination),
		n.Type == TypeP2P && (!hasSource || !hasDestination || n.SourceWalletID == n.DestinationWalletID):
		return fmt.Errorf("%w: %s", ErrInvalidWallets, n.Type)
	}

	switch {
	case n.InitiatorUserID == uuid.Nil:
		return fmt.Errorf("%w: initiator is required", ErrInvalidTransaction)
	case n.Amount.Currency() != money.EGP || n.Amount.Amount() <= 0:
		return fmt.Errorf("%w: amount must be a positive EGP amount", ErrInvalidTransaction)
	case n.IdempotencyKey == "":
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidTransaction)
	case utf8.RuneCountInString(n.Note) > maxNoteRunes:
		return fmt.Errorf("%w: note is longer than %d characters", ErrInvalidTransaction, maxNoteRunes)
	}
	return nil
}

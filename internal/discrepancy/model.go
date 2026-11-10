// Package discrepancy records problems an operator must look at: a bank outcome that
// contradicts a transaction, an amount mismatch, an event no transaction matches (TDR-016,
// TDR-009 §3). It owns reconciliation_discrepancies and never changes money or a
// transaction's status: it only records.
package discrepancy

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Kind is what went wrong (TDR-009 §3).
type Kind string

// Kinds of discrepancy.
const (
	KindStatusMismatch          Kind = "STATUS_MISMATCH"
	KindAmountMismatch          Kind = "AMOUNT_MISMATCH"
	KindUnmatchedBankMovement   Kind = "UNMATCHED_BANK_MOVEMENT"
	KindUnmatchedLedgerMovement Kind = "UNMATCHED_LEDGER_MOVEMENT"
	KindStuckTransaction        Kind = "STUCK_TRANSACTION"
	KindUnknownBankEvent        Kind = "UNKNOWN_BANK_EVENT"
	KindLedgerIntegrity         Kind = "LEDGER_INTEGRITY"
)

func (k Kind) valid() bool {
	switch k {
	case KindStatusMismatch, KindAmountMismatch, KindUnmatchedBankMovement, KindUnmatchedLedgerMovement,
		KindStuckTransaction, KindUnknownBankEvent, KindLedgerIntegrity:
		return true
	}
	return false
}

// Severity says how urgent the discrepancy is.
type Severity string

const (
	// SeverityCritical means money or a transaction's status may be wrong (status or amount mismatch).
	SeverityCritical Severity = "CRITICAL"
	// SeverityWarning means it needs a look but has no known money impact (e.g. an unknown bank event).
	SeverityWarning Severity = "WARNING"
)

func (s Severity) valid() bool { return s == SeverityCritical || s == SeverityWarning }

// Status is OPEN until an operator resolves the discrepancy (phase 2).
type Status string

// Discrepancy statuses.
const (
	StatusOpen     Status = "OPEN"
	StatusResolved Status = "RESOLVED"
)

// New is a discrepancy to record.
type New struct {
	Kind          Kind
	Severity      Severity
	TransactionID uuid.UUID // uuid.Nil when no transaction matches (e.g. UNKNOWN_BANK_EVENT)
	Details       any       // stored as JSON: what the operator needs to investigate
}

// Discrepancy is a recorded discrepancy.
type Discrepancy struct {
	ID             uuid.UUID
	Kind           Kind
	Severity       Severity
	TransactionID  uuid.UUID // uuid.Nil when none
	Details        json.RawMessage
	Status         Status
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	ResolutionNote string
}

// Filter selects discrepancies for List.
type Filter struct {
	Status Status  // "" means every status
	Kind   Kind    // "" means every kind
	Limit  int     // 1..MaxListLimit; 0 means DefaultListLimit
	After  *Cursor // nil: from the newest; else strictly older than this position
}

// Cursor is a position in the list: the (created_at, id) of the last row of a page.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Limits of Filter.Limit.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// Errors.
var (
	// ErrInvalid means a New or a Filter is not valid.
	ErrInvalid = errors.New("discrepancy: invalid")
	// ErrNotFound means no discrepancy has the ID.
	ErrNotFound = errors.New("discrepancy: not found")
	// ErrAlreadyResolved means the discrepancy is not OPEN any more.
	ErrAlreadyResolved = errors.New("discrepancy: already resolved")
)

// ValidKind reports whether k is a known kind.
func ValidKind(k Kind) bool { return k.valid() }

// Querier runs SQL: the pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

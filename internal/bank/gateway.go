// Package bank is the only way the wallet talks to a bank (TDR-008 §1): the Gateway port and
// its types. Adapters (internal/bank/banksim for the simulator, a real bank later) implement
// it and classify every failure as a rejection (business, do not retry), a temporary failure
// (retry) or "not found". Callers never see HTTP.
package bank

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Kind is the kind of a bank operation.
type Kind string

const (
	// KindDebit pulls money from the user's bank account (top-up).
	KindDebit Kind = "DEBIT"
	// KindPayout pushes money to the user's bank account (withdrawal).
	KindPayout Kind = "PAYOUT"
)

// OperationStatus is the bank's status of an operation.
type OperationStatus string

const (
	// StatusPending means the bank accepted the operation and has not settled it yet.
	StatusPending OperationStatus = "PENDING"
	// StatusSucceeded means the money moved.
	StatusSucceeded OperationStatus = "SUCCEEDED"
	// StatusFailed means the bank failed the operation after accepting it.
	StatusFailed OperationStatus = "FAILED"
)

// DebitRequest asks the bank to pull Amount from the account IBAN. IdempotencyKey is the
// wallet transaction ID (TDR-005 layer 3): repeating it never creates a second operation.
type DebitRequest struct {
	IdempotencyKey uuid.UUID
	Amount         money.Money
	IBAN           string
	HolderName     string
}

// PayoutRequest asks the bank to push Amount to the account IBAN. IdempotencyKey is the
// wallet transaction ID.
type PayoutRequest struct {
	IdempotencyKey uuid.UUID
	Amount         money.Money
	IBAN           string
	HolderName     string
}

// Acceptance is the bank's acknowledgement of a new operation.
type Acceptance struct {
	BankReference string
}

// Operation is the bank's current view of an operation.
type Operation struct {
	IdempotencyKey uuid.UUID
	BankReference  string
	Kind           Kind
	Status         OperationStatus
	Amount         money.Money
	FailureCode    string // set when Status is FAILED
	FailureMessage string
}

// Gateway is the bank port.
type Gateway interface {
	CreateDebit(ctx context.Context, req DebitRequest) (Acceptance, error)
	CreatePayout(ctx context.Context, req PayoutRequest) (Acceptance, error)
	// GetOperation returns ErrNotFound when the bank has no operation with the key.
	GetOperation(ctx context.Context, idempotencyKey uuid.UUID) (Operation, error)
}

// RejectedError is a business rejection by the bank (e.g. an invalid account). Retrying the
// same request gives the same answer. Use errors.As.
type RejectedError struct {
	Code    string
	Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("bank: rejected: %s: %s", e.Code, e.Message)
}

var (
	// ErrTemporary is a failure that may pass on a retry: timeout, connection error, 5xx,
	// 408/429, or an answer the adapter cannot read. It is wrapped with the details.
	ErrTemporary = errors.New("bank: temporary failure")
	// ErrNotFound means the bank has no operation with the idempotency key.
	ErrNotFound = errors.New("bank: operation not found")
)

// Outcome is the bank's final result for an operation, however we learned it: a webhook
// (TDR-007) or a status query during reconciliation (TDR-009). payment.ApplyBankOutcome
// applies it.
type Outcome struct {
	Kind           Kind
	Status         OperationStatus // StatusSucceeded or StatusFailed
	BankReference  string
	Amount         money.Money
	FailureCode    string // set when Status is StatusFailed
	FailureMessage string
}

// Final reports whether the outcome is a final result (succeeded or failed).
func (o Outcome) Final() bool { return o.Status == StatusSucceeded || o.Status == StatusFailed }

// Outcome returns the operation as an Outcome. It is final only once the operation settled.
func (op Operation) Outcome() Outcome {
	return Outcome{
		Kind: op.Kind, Status: op.Status, BankReference: op.BankReference, Amount: op.Amount,
		FailureCode: op.FailureCode, FailureMessage: op.FailureMessage,
	}
}

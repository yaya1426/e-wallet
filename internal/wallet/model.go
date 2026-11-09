// Package wallet owns the users' EGP wallets (PRD-001 §5.2). A wallet's money lives in its
// USER_WALLET ledger account; this module keeps the wallet record and reads the balance
// through the ledger (TDR-004 §5).
package wallet

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Status of a wallet (PRD-001 BR-8).
type Status string

// Wallet statuses.
const (
	StatusActive Status = "ACTIVE"
	StatusFrozen Status = "FROZEN"
)

// Wallet is a user's wallet record.
type Wallet struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	LedgerAccountID ledger.AccountID
	Currency        money.Currency
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// View is what a user sees of their wallet (PRD-001 US-3, BR-9).
type View struct {
	WalletID         uuid.UUID
	Currency         money.Currency
	AvailableBalance money.Money
	Status           Status
}

var (
	// ErrNotFound means the user has no wallet.
	ErrNotFound = errors.New("wallet: not found")
	// ErrMissingUserID means a wallet was requested without a user ID.
	ErrMissingUserID = errors.New("wallet: user ID is required")
)

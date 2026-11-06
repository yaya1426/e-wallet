// Package ledger is the double-entry ledger, the system of record for money and the only
// code that writes postings (TDR-004).
package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// AccountID identifies a ledger account.
type AccountID uuid.UUID

// String returns the canonical UUID text.
func (id AccountID) String() string { return uuid.UUID(id).String() }

// Kind says whether an account belongs to a user wallet or to the system.
type Kind string

// Account kinds.
const (
	KindUserWallet Kind = "USER_WALLET"
	KindSystem     Kind = "SYSTEM"
)

// AccountType is the accounting type of an account.
type AccountType string

// Account types.
const (
	TypeAsset     AccountType = "ASSET"
	TypeLiability AccountType = "LIABILITY"
)

// Direction is the side of a posting, and the normal side of an account.
type Direction string

// Directions.
const (
	Debit  Direction = "DEBIT"
	Credit Direction = "CREDIT"
)

// System account codes (TDR-004 §2). They are created by the ledger migration.
const (
	CodeBankSettlement       = "BANK_SETTLEMENT"
	CodeWithdrawalsInTransit = "WITHDRAWALS_IN_TRANSIT"
	CodeSuspense             = "SUSPENSE"
)

// IDs of the system accounts, fixed by the ledger migration so they are identical in every
// environment. Post uses them to apply system-account postings last (see Post).
var systemAccountIDs = map[AccountID]bool{
	AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000001")): true, // BANK_SETTLEMENT
	AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000002")): true, // WITHDRAWALS_IN_TRANSIT
	AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000003")): true, // SUSPENSE
}

// userWalletCodePrefix + wallet ID is the code of a user wallet account.
const userWalletCodePrefix = "USER_WALLET:"

// EntryType is the business step a journal entry records. A transaction has at most one
// entry of each type (TDR-004 §4).
type EntryType string

// Entry types (TDR-004 §3).
const (
	EntryTopUpSettle       EntryType = "TOPUP_SETTLE"
	EntryWithdrawalReserve EntryType = "WITHDRAWAL_RESERVE"
	EntryWithdrawalSettle  EntryType = "WITHDRAWAL_SETTLE"
	EntryWithdrawalRefund  EntryType = "WITHDRAWAL_REFUND"
	EntryP2P               EntryType = "P2P"
	EntryAdjustment        EntryType = "ADJUSTMENT"
)

func (t EntryType) valid() bool {
	switch t {
	case EntryTopUpSettle, EntryWithdrawalReserve, EntryWithdrawalSettle,
		EntryWithdrawalRefund, EntryP2P, EntryAdjustment:
		return true
	default:
		return false
	}
}

// Account is a ledger account with its cached balance, signed in its normal side
// (TDR-004 §4-5).
type Account struct {
	ID         AccountID
	Code       string
	Kind       Kind
	Type       AccountType
	NormalSide Direction
	Balance    money.Money
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Entry is a balanced group of postings for one business step of a transaction.
type Entry struct {
	TransactionID uuid.UUID
	Type          EntryType
	Description   string
	Postings      []Posting
}

// Posting is one line of an entry. Amount is always positive; Direction gives the sign.
type Posting struct {
	AccountID AccountID
	Direction Direction
	Amount    money.Money
}

// PostResult describes a posted entry.
type PostResult struct {
	EntryID uuid.UUID
	// BalancesAfter is each touched account's cached balance after the entry.
	BalancesAfter map[AccountID]int64
}

var (
	// ErrTooFewPostings means an entry has fewer than two postings.
	ErrTooFewPostings = errors.New("ledger: an entry needs at least two postings")
	// ErrNonPositiveAmount means a posting amount is zero or negative.
	ErrNonPositiveAmount = errors.New("ledger: posting amount must be positive")
	// ErrCurrencyMismatch means the postings of an entry are not all in one currency.
	ErrCurrencyMismatch = errors.New("ledger: postings use different currencies")
	// ErrUnbalanced means the debits of an entry do not equal its credits.
	ErrUnbalanced = errors.New("ledger: entry is not balanced")
	// ErrUnknownEntryType means the entry type is not one of the defined types.
	ErrUnknownEntryType = errors.New("ledger: unknown entry type")
	// ErrMissingTransactionID means an entry has no transaction ID.
	ErrMissingTransactionID = errors.New("ledger: entry needs a transaction ID")
	// ErrMissingWalletID means a user account was requested without a wallet ID.
	ErrMissingWalletID = errors.New("ledger: user account needs a wallet ID")
	// ErrInvalidDirection means a posting direction is neither DEBIT nor CREDIT.
	ErrInvalidDirection = errors.New("ledger: invalid posting direction")
	// ErrDuplicateEntry means an entry of this type already exists for the transaction:
	// the business step was already applied (TDR-005 layer 4). The DB transaction is aborted.
	ErrDuplicateEntry = errors.New("ledger: entry already posted for this transaction")
	// ErrInsufficientBalance means a posting would make a user wallet negative. Callers
	// check the balance under lock first; this is the last-line guard (TDR-010 §4).
	ErrInsufficientBalance = errors.New("ledger: insufficient balance")
	// ErrAccountNotFound means an account ID or code does not exist.
	ErrAccountNotFound = errors.New("ledger: account not found")
	// ErrAccountExists means a user wallet account already exists for the wallet.
	ErrAccountExists = errors.New("ledger: account already exists")
)

// validate checks an entry before any SQL runs.
func (e Entry) validate() error {
	if !e.Type.valid() {
		return fmt.Errorf("%w: %q", ErrUnknownEntryType, e.Type)
	}
	if e.TransactionID == uuid.Nil {
		return ErrMissingTransactionID
	}
	if len(e.Postings) < 2 {
		return fmt.Errorf("%w: got %d", ErrTooFewPostings, len(e.Postings))
	}
	currency := e.Postings[0].Amount.Currency()
	debits, err := money.New(0, currency)
	if err != nil {
		return fmt.Errorf("ledger: posting 0: %w", err) // e.g. money.ErrUnsupportedCurrency
	}
	credits := debits
	for i, p := range e.Postings {
		if p.Amount.Currency() != currency {
			return fmt.Errorf("%w: posting %d is %q, expected %q", ErrCurrencyMismatch, i, p.Amount.Currency(), currency)
		}
		if p.Amount.Amount() <= 0 {
			return fmt.Errorf("%w: posting %d has %d", ErrNonPositiveAmount, i, p.Amount.Amount())
		}
		switch p.Direction {
		case Debit:
			debits, err = debits.Add(p.Amount)
		case Credit:
			credits, err = credits.Add(p.Amount)
		default:
			return fmt.Errorf("%w: posting %d is %q", ErrInvalidDirection, i, p.Direction)
		}
		if err != nil {
			return fmt.Errorf("ledger: sum postings: %w", err)
		}
	}
	if debits != credits {
		return fmt.Errorf("%w: debits %s, credits %s", ErrUnbalanced, debits, credits)
	}
	return nil
}

// IntegrityReport lists every violation of the ledger invariants found by VerifyIntegrity
// (TDR-004 §6 items 1-2, data-model invariants 1-3).
type IntegrityReport struct {
	TotalDebits       int64
	TotalCredits      int64
	Drifts            []AccountDrift
	UnbalancedEntries []UnbalancedEntry
}

// OK reports whether no violation was found.
func (r IntegrityReport) OK() bool {
	return r.TotalDebits == r.TotalCredits && len(r.Drifts) == 0 && len(r.UnbalancedEntries) == 0
}

// AccountDrift is an account whose cached balance differs from its postings.
type AccountDrift struct {
	AccountID AccountID
	Code      string
	Cached    int64
	Computed  int64
}

// UnbalancedEntry is a journal entry whose debits differ from its credits, or that has
// fewer than two postings.
type UnbalancedEntry struct {
	EntryID  uuid.UUID
	Debits   int64
	Credits  int64
	Postings int64
}

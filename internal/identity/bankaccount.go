package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/identity/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// BankAccount is a user's linked bank account (PRD-001 US-6). The IBAN is personal data:
// it must not be logged.
type BankAccount struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	IBAN       string // normalized, e.g. EG890003000100000000123450000
	HolderName string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

var (
	// ErrInvalidHolderName means the trimmed holder name is shorter than 2 or longer than 100 characters.
	ErrInvalidHolderName = errors.New("identity: invalid holder name")
	// ErrBankAccountNotFound means the user has not linked a bank account.
	ErrBankAccountNotFound = errors.New("identity: bank account not found")
)

// LinkBankAccount links the user's bank account, replacing an existing one (one per user,
// PRD-001 Q4). Validation order: IBAN, then holder name. Replacing keeps the row's ID and
// created_at; transactions in flight keep their own snapshot (T017/T018).
func (s *Service) LinkBankAccount(ctx context.Context, userID uuid.UUID, iban, holderName string) (BankAccount, error) {
	normalized, err := NormalizeIBAN(iban)
	if err != nil {
		return BankAccount{}, err
	}
	holderName = strings.TrimSpace(holderName)
	if n := utf8.RuneCountInString(holderName); n < minNameRunes || n > maxNameRunes {
		return BankAccount{}, ErrInvalidHolderName
	}
	row, err := queries(s.db.Pool()).UpsertBankAccount(ctx, sqlcgen.UpsertBankAccountParams{
		ID:         id.New(),
		UserID:     userID,
		Iban:       normalized,
		HolderName: holderName,
		Now:        s.clock.Now().Truncate(time.Microsecond),
	})
	if err != nil {
		return BankAccount{}, fmt.Errorf("identity: link bank account: %w", err)
	}
	return toBankAccount(row), nil
}

// GetBankAccount returns the user's linked bank account, or ErrBankAccountNotFound. q is a
// pgx.Tx (e.g. the top-up and withdrawal transactions) or the pool.
func (s *Service) GetBankAccount(ctx context.Context, q Querier, userID uuid.UUID) (BankAccount, error) {
	row, err := queries(q).GetBankAccountByUserID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, ErrBankAccountNotFound
	}
	if err != nil {
		return BankAccount{}, fmt.Errorf("identity: get bank account: %w", err)
	}
	return toBankAccount(row), nil
}

// LinkedBankAccount returns the user's bank account, read outside any unit of work (e.g. for
// the HTTP API).
func (s *Service) LinkedBankAccount(ctx context.Context, userID uuid.UUID) (BankAccount, error) {
	return s.GetBankAccount(ctx, s.db.Pool(), userID)
}

func toBankAccount(row sqlcgen.BankAccount) BankAccount {
	return BankAccount{
		ID:         row.ID,
		UserID:     row.UserID,
		IBAN:       row.Iban,
		HolderName: row.HolderName,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

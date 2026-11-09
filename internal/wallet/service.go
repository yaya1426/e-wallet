package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/wallet/internal/sqlcgen"
)

// Service is the wallet API used by other modules and the HTTP handler.
type Service struct {
	db     *database.DB
	ledger *ledger.Service
	clock  clock.Clock
}

// NewService returns a wallet service. db is used for reads outside a unit of work.
func NewService(db *database.DB, ledgerService *ledger.Service, clk clock.Clock) *Service {
	return &Service{db: db, ledger: ledgerService, clock: clk}
}

// Open creates the user's EGP wallet and its USER_WALLET ledger account (balance 0) inside
// tx, so they commit together with the user (PRD-001 BR-5). It returns the wallet ID.
func (s *Service) Open(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	if userID == uuid.Nil {
		return uuid.Nil, ErrMissingUserID
	}
	walletID := id.New()
	accountID, err := s.ledger.CreateUserAccount(ctx, tx, walletID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("wallet: open: %w", err)
	}
	err = queries(tx).CreateWallet(ctx, sqlcgen.CreateWalletParams{
		ID:              walletID,
		UserID:          userID,
		LedgerAccountID: uuid.UUID(accountID),
		Now:             s.clock.Now(),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("wallet: open: insert wallet: %w", err)
	}
	return walletID, nil
}

// GetByUserID returns the user's wallet. q is a pgx.Tx or the pool.
func (s *Service) GetByUserID(ctx context.Context, q Querier, userID uuid.UUID) (Wallet, error) {
	row, err := queries(q).GetWalletByUserID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Wallet{}, fmt.Errorf("%w: user %s", ErrNotFound, userID)
	}
	if err != nil {
		return Wallet{}, fmt.Errorf("wallet: get by user %s: %w", userID, err)
	}
	return toWallet(row), nil
}

// GetByID returns the wallet with the given ID. q is a pgx.Tx or the pool.
func (s *Service) GetByID(ctx context.Context, q Querier, walletID uuid.UUID) (Wallet, error) {
	row, err := queries(q).GetWalletByID(ctx, walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Wallet{}, fmt.Errorf("%w: wallet %s", ErrNotFound, walletID)
	}
	if err != nil {
		return Wallet{}, fmt.Errorf("wallet: get %s: %w", walletID, err)
	}
	return toWallet(row), nil
}

// View returns the user's wallet with its available balance, the cached ledger balance
// (TDR-004 §5, PRD-001 BR-9).
func (s *Service) View(ctx context.Context, userID uuid.UUID) (View, error) {
	w, err := s.GetByUserID(ctx, s.db.Pool(), userID)
	if err != nil {
		return View{}, err
	}
	balance, err := s.ledger.Balance(ctx, s.db.Pool(), w.LedgerAccountID)
	if err != nil {
		return View{}, fmt.Errorf("wallet: view balance: %w", err)
	}
	return View{WalletID: w.ID, Currency: w.Currency, AvailableBalance: balance, Status: w.Status}, nil
}

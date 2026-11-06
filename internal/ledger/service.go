package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/ledger/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/id"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Service is the ledger API used by other modules. Writes take the caller's pgx.Tx so they
// commit atomically with the caller's other changes (TDR-001 §3).
type Service struct {
	db    *database.DB
	clock clock.Clock
}

// NewService returns a ledger service. db is used for reads outside a unit of work.
func NewService(db *database.DB, clk clock.Clock) *Service {
	return &Service{db: db, clock: clk}
}

// CreateUserAccount creates the ledger account of a wallet: code "USER_WALLET:<walletID>",
// a credit-normal liability with balance 0. A second account for the same wallet returns
// ErrAccountExists.
func (s *Service) CreateUserAccount(ctx context.Context, tx pgx.Tx, walletID uuid.UUID) (AccountID, error) {
	if walletID == uuid.Nil {
		return AccountID{}, ErrMissingWalletID
	}
	accountID := id.New()
	err := queries(tx).CreateAccount(ctx, sqlcgen.CreateAccountParams{
		ID:         accountID,
		Code:       userWalletCodePrefix + walletID.String(),
		Kind:       string(KindUserWallet),
		Type:       string(TypeLiability),
		NormalSide: string(Credit),
		Now:        s.clock.Now(),
	})
	if violates(err, codeUniqueViolation, constraintAccountCode) {
		return AccountID{}, fmt.Errorf("%w: wallet %s", ErrAccountExists, walletID)
	}
	if err != nil {
		return AccountID{}, fmt.Errorf("ledger: create user account: %w", err)
	}
	return AccountID(accountID), nil
}

// LockAccounts locks the accounts with SELECT … FOR UPDATE in ascending ID order (TDR-010
// §1-2) and returns them. Callers that check a user-wallet balance must lock it first, in
// the same transaction that posts. Pass only user-wallet accounts: system accounts are
// locked by Post's UPDATE, after every user-wallet account (see Post). A missing ID returns
// ErrAccountNotFound.
func (s *Service) LockAccounts(ctx context.Context, tx pgx.Tx, ids []AccountID) (map[AccountID]Account, error) {
	unique := slices.Clone(ids)
	slices.SortFunc(unique, compareIDs)
	unique = slices.Compact(unique)

	rows, err := queries(tx).LockAccounts(ctx, uuids(unique))
	if err != nil {
		return nil, fmt.Errorf("ledger: lock accounts: %w", err)
	}
	accounts := make(map[AccountID]Account, len(rows))
	for _, row := range rows {
		account, err := toAccount(row)
		if err != nil {
			return nil, err
		}
		accounts[account.ID] = account
	}
	for _, accountID := range unique {
		if _, ok := accounts[accountID]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
		}
	}
	return accounts, nil
}

// Post writes a balanced entry and updates each account's cached balance, version and the
// postings' balance_after, all inside tx (TDR-004 §4-5).
//
// Lock order: each UPDATE keeps its row locked until the transaction ends, so Post touches
// user-wallet accounts first (ascending ID) and system accounts last (ascending ID). That is
// the global order (TDR-010 §2-3): a caller that pre-locked its user accounts with
// LockAccounts and a caller that did not both take user rows before system rows, so they
// cannot deadlock.
//
// It returns ErrDuplicateEntry when the transaction already has an entry of this type,
// ErrInsufficientBalance when a user wallet would go negative and ErrAccountNotFound for an
// unknown account; after any of them the DB transaction is aborted and must be rolled back.
func (s *Service) Post(ctx context.Context, tx pgx.Tx, e Entry) (PostResult, error) {
	if err := e.validate(); err != nil {
		return PostResult{}, err
	}
	now := s.clock.Now()
	q := queries(tx)

	entryID := id.New()
	err := q.InsertJournalEntry(ctx, sqlcgen.InsertJournalEntryParams{
		ID:            entryID,
		TransactionID: e.TransactionID,
		EntryType:     string(e.Type),
		Description:   e.Description,
		CreatedAt:     now,
	})
	if violates(err, codeUniqueViolation, constraintEntryPerTransaction) {
		return PostResult{}, fmt.Errorf("%w: transaction %s, %s", ErrDuplicateEntry, e.TransactionID, e.Type)
	}
	if err != nil {
		return PostResult{}, fmt.Errorf("ledger: insert journal entry: %w", err)
	}

	// Touch accounts in the global lock order so concurrent entries cannot deadlock (see above).
	postings := slices.Clone(e.Postings)
	slices.SortStableFunc(postings, func(a, b Posting) int { return compareLockOrder(a.AccountID, b.AccountID) })

	balances := make(map[AccountID]int64, len(postings))
	for _, p := range postings {
		balance, err := q.ApplyPosting(ctx, sqlcgen.ApplyPostingParams{
			ID:        uuid.UUID(p.AccountID),
			Direction: string(p.Direction),
			Amount:    p.Amount.Amount(),
			Now:       now,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return PostResult{}, fmt.Errorf("%w: %s", ErrAccountNotFound, p.AccountID)
		case violates(err, codeCheckViolation, constraintUserWalletNonNegative):
			return PostResult{}, fmt.Errorf("%w: account %s", ErrInsufficientBalance, p.AccountID)
		case err != nil:
			return PostResult{}, fmt.Errorf("ledger: apply posting to %s: %w", p.AccountID, err)
		}
		err = q.InsertPosting(ctx, sqlcgen.InsertPostingParams{
			ID:             id.New(),
			JournalEntryID: entryID,
			AccountID:      uuid.UUID(p.AccountID),
			Direction:      string(p.Direction),
			Amount:         p.Amount.Amount(),
			Currency:       string(p.Amount.Currency()),
			BalanceAfter:   balance,
			CreatedAt:      now,
		})
		if err != nil {
			return PostResult{}, fmt.Errorf("ledger: insert posting: %w", err)
		}
		balances[p.AccountID] = balance
	}
	return PostResult{EntryID: entryID, BalancesAfter: balances}, nil
}

// Balance returns the cached balance of an account, signed in its normal side. q is a
// pgx.Tx or the pool.
func (s *Service) Balance(ctx context.Context, q Querier, accountID AccountID) (money.Money, error) {
	account, err := s.account(ctx, q, accountID)
	if err != nil {
		return money.Money{}, err
	}
	return account.Balance, nil
}

// GetAccountByCode returns the account with the given code, e.g. CodeBankSettlement.
func (s *Service) GetAccountByCode(ctx context.Context, q Querier, code string) (Account, error) {
	row, err := queries(q).GetAccountByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: %q", ErrAccountNotFound, code)
	}
	if err != nil {
		return Account{}, fmt.Errorf("ledger: get account %q: %w", code, err)
	}
	return toAccount(row)
}

// VerifyIntegrity checks the ledger invariants on committed data (TDR-004 §6 items 1-2):
// all debits equal all credits, every cached balance equals its postings, and every entry
// balances. Item 3 (withdrawals in transit) needs the transaction module (phase 2).
func (s *Service) VerifyIntegrity(ctx context.Context) (IntegrityReport, error) {
	q := queries(s.db.Pool())
	var report IntegrityReport

	totals, err := q.PostingTotals(ctx)
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("ledger: posting totals: %w", err)
	}
	report.TotalDebits, report.TotalCredits = totals.Debits, totals.Credits

	drifts, err := q.AccountDrifts(ctx)
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("ledger: account drifts: %w", err)
	}
	for _, d := range drifts {
		report.Drifts = append(report.Drifts, AccountDrift{AccountID: AccountID(d.ID), Code: d.Code, Cached: d.Cached, Computed: d.Computed})
	}

	unbalanced, err := q.UnbalancedEntries(ctx)
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("ledger: unbalanced entries: %w", err)
	}
	for _, u := range unbalanced {
		report.UnbalancedEntries = append(report.UnbalancedEntries, UnbalancedEntry{EntryID: u.ID, Debits: u.Debits, Credits: u.Credits, Postings: u.Postings})
	}
	return report, nil
}

func (s *Service) account(ctx context.Context, q Querier, accountID AccountID) (Account, error) {
	row, err := queries(q).GetAccount(ctx, uuid.UUID(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	if err != nil {
		return Account{}, fmt.Errorf("ledger: get account %s: %w", accountID, err)
	}
	return toAccount(row)
}

// compareIDs orders account IDs by their bytes, the order PostgreSQL uses for uuid.
func compareIDs(a, b AccountID) int { return bytes.Compare(a[:], b[:]) }

// compareLockOrder is the global lock order: user-wallet accounts before system accounts,
// each group by ascending ID.
func compareLockOrder(a, b AccountID) int {
	if sa, sb := systemAccountIDs[a], systemAccountIDs[b]; sa != sb {
		if sa {
			return 1
		}
		return -1
	}
	return compareIDs(a, b)
}

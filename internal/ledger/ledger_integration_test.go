//go:build integration

package ledger_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/ledger/ledgertest"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

type fixture struct {
	db    *database.DB
	svc   *ledger.Service
	clock *clocktest.Fake
}

// setup migrates a fresh database. Unless skipIntegrity is set, the ledger integrity
// check runs when the test ends (PRD-001 §9.3).
func setup(t *testing.T, skipIntegrity ...bool) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))

	if len(skipIntegrity) == 0 || !skipIntegrity[0] {
		t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })
	}
	fake := clocktest.New(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	return fixture{db: db, svc: ledger.NewService(db, fake), clock: fake}
}

func egp(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(amount, money.EGP)
	require.NoError(t, err)
	return m
}

func (f fixture) userAccount(t *testing.T) ledger.AccountID {
	t.Helper()
	var accountID ledger.AccountID
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		accountID, err = f.svc.CreateUserAccount(ctx, tx, uuid.New())
		return err
	})
	require.NoError(t, err)
	return accountID
}

func (f fixture) system(t *testing.T, code string) ledger.Account {
	t.Helper()
	account, err := f.svc.GetAccountByCode(context.Background(), f.db.Pool(), code)
	require.NoError(t, err)
	return account
}

func (f fixture) post(ctx context.Context, e ledger.Entry, lock ...ledger.AccountID) (ledger.PostResult, error) {
	var result ledger.PostResult
	err := f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if len(lock) > 0 {
			if _, err := f.svc.LockAccounts(ctx, tx, lock); err != nil {
				return err
			}
		}
		var err error
		result, err = f.svc.Post(ctx, tx, e)
		return err
	})
	return result, err
}

// fund credits a user account from the bank settlement account (a top-up settlement).
func (f fixture) fund(t *testing.T, user ledger.AccountID, amount int64) {
	t.Helper()
	bank := f.system(t, ledger.CodeBankSettlement)
	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryTopUpSettle,
		Postings: []ledger.Posting{
			{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, amount)},
			{AccountID: user, Direction: ledger.Credit, Amount: egp(t, amount)},
		},
	}, user)
	require.NoError(t, err)
}

func (f fixture) balance(t *testing.T, accountID ledger.AccountID) int64 {
	t.Helper()
	b, err := f.svc.Balance(context.Background(), f.db.Pool(), accountID)
	require.NoError(t, err)
	return b.Amount()
}

func (f fixture) version(t *testing.T, accountID ledger.AccountID) int64 {
	t.Helper()
	var v int64
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT version FROM ledger_accounts WHERE id = $1`, uuid.UUID(accountID)).Scan(&v))
	return v
}

func TestMigration_LedgerSchemaAndSeed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	for _, table := range []string{"ledger_accounts", "journal_entries", "postings"} {
		var exists bool
		require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		assert.True(t, exists, table)
	}
	seed := []struct {
		code       string
		typ        ledger.AccountType
		normalSide ledger.Direction
	}{
		{ledger.CodeBankSettlement, ledger.TypeAsset, ledger.Debit},
		{ledger.CodeWithdrawalsInTransit, ledger.TypeLiability, ledger.Credit},
		{ledger.CodeSuspense, ledger.TypeLiability, ledger.Credit},
	}
	for _, s := range seed {
		account := f.system(t, s.code)
		assert.Equal(t, ledger.KindSystem, account.Kind, s.code)
		assert.Equal(t, s.typ, account.Type, s.code)
		assert.Equal(t, s.normalSide, account.NormalSide, s.code)
		assert.Equal(t, int64(0), account.Balance.Amount(), s.code)
		assert.Equal(t, money.EGP, account.Balance.Currency(), s.code)
	}

	migrator, err := database.NewMigrator(f.db.Pool(), "")
	require.NoError(t, err)
	status, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, status.Version, uint(2), "the ledger migration (000002) is applied")
}

func TestCreateUserAccount(t *testing.T) {
	f := setup(t)
	walletID := uuid.New()

	var accountID ledger.AccountID
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		accountID, err = f.svc.CreateUserAccount(ctx, tx, walletID)
		return err
	})
	require.NoError(t, err)

	account, err := f.svc.GetAccountByCode(context.Background(), f.db.Pool(), "USER_WALLET:"+walletID.String())
	require.NoError(t, err)
	assert.Equal(t, accountID, account.ID)
	assert.Equal(t, ledger.KindUserWallet, account.Kind)
	assert.Equal(t, ledger.TypeLiability, account.Type)
	assert.Equal(t, ledger.Credit, account.NormalSide)
	assert.Equal(t, int64(0), account.Balance.Amount())
	assert.Equal(t, f.clock.Now(), account.CreatedAt.UTC())
	assert.Equal(t, uuid.Version(7), uuid.UUID(accountID).Version())
}

func TestCreateUserAccount_Duplicate(t *testing.T) {
	f := setup(t)
	walletID := uuid.New()
	create := func() error {
		return f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.svc.CreateUserAccount(ctx, tx, walletID)
			return err
		})
	}

	require.NoError(t, create())
	assert.ErrorIs(t, create(), ledger.ErrAccountExists)
}

func TestPost_TopUpSettle(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	bank := f.system(t, ledger.CodeBankSettlement)
	txnID := uuid.New()

	result, err := f.post(context.Background(), ledger.Entry{
		TransactionID: txnID,
		Type:          ledger.EntryTopUpSettle,
		Description:   "top-up",
		Postings: []ledger.Posting{
			{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, 1000)},
			{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 1000)},
		},
	}, user)
	require.NoError(t, err)

	assert.Equal(t, int64(1000), f.balance(t, user))
	assert.Equal(t, int64(1000), f.balance(t, bank.ID), "debit-normal asset grows with a debit")
	assert.Equal(t, map[ledger.AccountID]int64{user: 1000, bank.ID: 1000}, result.BalancesAfter)
	assert.Equal(t, int64(1), f.version(t, user))
	assert.Equal(t, int64(1), f.version(t, bank.ID))

	rows, err := f.db.Pool().Query(context.Background(),
		`SELECT p.account_id, p.direction, p.amount, p.balance_after, e.transaction_id, e.entry_type
		   FROM postings p JOIN journal_entries e ON e.id = p.journal_entry_id WHERE e.id = $1`, result.EntryID)
	require.NoError(t, err)
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var (
			accountID        uuid.UUID
			direction, eType string
			amount, after    int64
			gotTxn           uuid.UUID
		)
		require.NoError(t, rows.Scan(&accountID, &direction, &amount, &after, &gotTxn, &eType))
		assert.Equal(t, int64(1000), amount)
		assert.Equal(t, int64(1000), after)
		assert.Equal(t, txnID, gotTxn)
		assert.Equal(t, "TOPUP_SETTLE", eType)
		seen++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, seen)
}

func TestPost_WithdrawalReserve(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	f.fund(t, user, 1000)
	inTransit := f.system(t, ledger.CodeWithdrawalsInTransit)

	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryWithdrawalReserve,
		Postings: []ledger.Posting{
			{AccountID: user, Direction: ledger.Debit, Amount: egp(t, 1000)},
			{AccountID: inTransit.ID, Direction: ledger.Credit, Amount: egp(t, 1000)},
		},
	}, user)
	require.NoError(t, err)

	assert.Equal(t, int64(0), f.balance(t, user))
	assert.Equal(t, int64(1000), f.balance(t, inTransit.ID))
}

func TestPost_InvalidEntryWritesNothing(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	bank := f.system(t, ledger.CodeBankSettlement)

	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryTopUpSettle,
		Postings: []ledger.Posting{
			{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, 1000)},
			{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 999)},
		},
	})
	require.ErrorIs(t, err, ledger.ErrUnbalanced)

	var entries int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM journal_entries`).Scan(&entries))
	assert.Zero(t, entries)
	assert.Equal(t, int64(0), f.balance(t, user))
}

func TestPost_DuplicateEntry(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	bank := f.system(t, ledger.CodeBankSettlement)
	entry := ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryTopUpSettle,
		Postings: []ledger.Posting{
			{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, 500)},
			{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 500)},
		},
	}

	_, err := f.post(context.Background(), entry, user)
	require.NoError(t, err)
	_, err = f.post(context.Background(), entry, user)
	require.ErrorIs(t, err, ledger.ErrDuplicateEntry)

	assert.Equal(t, int64(500), f.balance(t, user), "applied once")

	// The same transaction may still have an entry of another type.
	_, err = f.post(context.Background(), ledger.Entry{
		TransactionID: entry.TransactionID,
		Type:          ledger.EntryAdjustment,
		Postings: []ledger.Posting{
			{AccountID: user, Direction: ledger.Debit, Amount: egp(t, 500)},
			{AccountID: bank.ID, Direction: ledger.Credit, Amount: egp(t, 500)},
		},
	}, user)
	require.NoError(t, err)
}

func TestPost_InsufficientBalance(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	f.fund(t, user, 999)
	inTransit := f.system(t, ledger.CodeWithdrawalsInTransit)

	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryWithdrawalReserve,
		Postings: []ledger.Posting{
			{AccountID: user, Direction: ledger.Debit, Amount: egp(t, 1000)},
			{AccountID: inTransit.ID, Direction: ledger.Credit, Amount: egp(t, 1000)},
		},
	}, user)

	require.ErrorIs(t, err, ledger.ErrInsufficientBalance)
	assert.Equal(t, int64(999), f.balance(t, user), "rolled back")
	assert.Equal(t, int64(0), f.balance(t, inTransit.ID))
}

func TestPost_SystemAccountNegativeAllowed(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	inTransit := f.system(t, ledger.CodeWithdrawalsInTransit)

	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryWithdrawalRefund,
		Postings: []ledger.Posting{
			{AccountID: inTransit.ID, Direction: ledger.Debit, Amount: egp(t, 300)},
			{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 300)},
		},
	}, user)

	require.NoError(t, err)
	assert.Equal(t, int64(-300), f.balance(t, inTransit.ID))
	assert.Equal(t, int64(300), f.balance(t, user))
}

func TestPost_UnknownAccount(t *testing.T) {
	f := setup(t)
	bank := f.system(t, ledger.CodeBankSettlement)

	_, err := f.post(context.Background(), ledger.Entry{
		TransactionID: uuid.New(),
		Type:          ledger.EntryTopUpSettle,
		Postings: []ledger.Posting{
			{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, 1)},
			{AccountID: ledger.AccountID(uuid.New()), Direction: ledger.Credit, Amount: egp(t, 1)},
		},
	})

	require.ErrorIs(t, err, ledger.ErrAccountNotFound)
}

// rawEntry inserts a journal entry and postings with plain SQL, bypassing Post.
func rawEntry(ctx context.Context, tx pgx.Tx, postings ...struct {
	account   ledger.AccountID
	direction string
	amount    int64
}) error {
	entryID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO journal_entries (id, transaction_id, entry_type, created_at) VALUES ($1, $2, 'ADJUSTMENT', now())`, entryID, uuid.New()); err != nil {
		return err
	}
	for _, p := range postings {
		if _, err := tx.Exec(ctx, `INSERT INTO postings (id, journal_entry_id, account_id, direction, amount, balance_after, created_at)
			VALUES ($1, $2, $3, $4, $5, 0, now())`, uuid.New(), entryID, uuid.UUID(p.account), p.direction, p.amount); err != nil {
			return err
		}
	}
	return nil
}

func TestTrigger_UnbalancedCommitFails(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	bank := f.system(t, ledger.CodeBankSettlement)
	suspense := f.system(t, ledger.CodeSuspense)
	type line = struct {
		account   ledger.AccountID
		direction string
		amount    int64
	}
	tests := map[string][]line{
		"unbalanced":  {{bank.ID, "DEBIT", 100}, {suspense.ID, "CREDIT", 99}},
		"one posting": {{bank.ID, "DEBIT", 100}},
		"no postings": nil,
	}
	for name, lines := range tests {
		t.Run(name, func(t *testing.T) {
			tx, err := f.db.Pool().Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx) //nolint:errcheck // already ended by the failed commit

			require.NoError(t, rawEntry(ctx, tx, lines...), "inserts pass; the check is deferred to commit")
			err = tx.Commit(ctx)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "23514", pgErr.Code)
			assert.Equal(t, "journal_entry_balanced", pgErr.ConstraintName)
		})
	}

	var entries int
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&entries))
	assert.Zero(t, entries, "nothing was committed")
}

func TestTrigger_AppendOnly(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	user := f.userAccount(t)
	f.fund(t, user, 100)

	for _, stmt := range []string{
		`UPDATE postings SET amount = amount + 1`,
		`DELETE FROM postings`,
		`TRUNCATE postings`,
		`UPDATE journal_entries SET description = 'changed'`,
		`DELETE FROM journal_entries`,
		`TRUNCATE journal_entries CASCADE`,
	} {
		_, err := f.db.Pool().Exec(ctx, stmt)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, stmt)
		assert.Equal(t, "23001", pgErr.Code, stmt) // restrict_violation
		assert.Contains(t, pgErr.Message, "append-only", stmt)
	}
	assert.Equal(t, int64(100), f.balance(t, user))
}

func TestPost_ConcurrentCredits(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	bank := f.system(t, ledger.CodeBankSettlement)
	const n = 50

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.post(context.Background(), ledger.Entry{
				TransactionID: uuid.New(),
				Type:          ledger.EntryTopUpSettle,
				Postings: []ledger.Posting{
					{AccountID: bank.ID, Direction: ledger.Debit, Amount: egp(t, 100)},
					{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 100)},
				},
			}, user)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, int64(n*100), f.balance(t, user))
	assert.Equal(t, int64(n*100), f.balance(t, bank.ID))
	assert.Equal(t, int64(n+0), f.version(t, user))
}

func TestPost_OppositeTransfersNoDeadlock(t *testing.T) {
	f := setup(t)
	a, b := f.userAccount(t), f.userAccount(t)
	f.fund(t, a, 10_000)
	f.fund(t, b, 10_000)
	const rounds = 50

	transfer := func(from, to ledger.AccountID) error {
		_, err := f.post(context.Background(), ledger.Entry{
			TransactionID: uuid.New(),
			Type:          ledger.EntryP2P,
			Postings: []ledger.Posting{
				{AccountID: from, Direction: ledger.Debit, Amount: egp(t, 10)},
				{AccountID: to, Direction: ledger.Credit, Amount: egp(t, 10)},
			},
		}, from, to)
		return err
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, dir := range [][2]ledger.AccountID{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				errs <- transfer(dir[0], dir[1])
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "ordered locks: no deadlock reaches the caller")
	}

	assert.Equal(t, int64(10_000), f.balance(t, a))
	assert.Equal(t, int64(10_000), f.balance(t, b))
}

func TestVerifyIntegrity_Clean(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	f.fund(t, user, 2500)

	report, err := f.svc.VerifyIntegrity(context.Background())
	require.NoError(t, err)
	assert.True(t, report.OK())
	assert.Equal(t, int64(2500), report.TotalDebits)
	assert.Equal(t, int64(2500), report.TotalCredits)
}

func TestVerifyIntegrity_DetectsDrift(t *testing.T) {
	f := setup(t, true) // this test breaks the ledger on purpose
	user := f.userAccount(t)
	f.fund(t, user, 1000)

	// The append-only trigger covers entries and postings; a cached balance can still be
	// changed by raw SQL, which is exactly what the integrity check must catch.
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE ledger_accounts SET balance = balance + 7 WHERE id = $1`, uuid.UUID(user))
	require.NoError(t, err)

	report, err := f.svc.VerifyIntegrity(context.Background())
	require.NoError(t, err)
	assert.False(t, report.OK())
	require.Len(t, report.Drifts, 1)
	assert.Equal(t, user, report.Drifts[0].AccountID)
	assert.Equal(t, int64(1007), report.Drifts[0].Cached)
	assert.Equal(t, int64(1000), report.Drifts[0].Computed)
	assert.Empty(t, report.UnbalancedEntries)
	assert.Equal(t, report.TotalDebits, report.TotalCredits)
}

func TestLockAccounts(t *testing.T) {
	f := setup(t)
	a, b := f.userAccount(t), f.userAccount(t)

	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		accounts, err := f.svc.LockAccounts(ctx, tx, []ledger.AccountID{b, a, b})
		if err != nil {
			return err
		}
		assert.Len(t, accounts, 2)
		assert.Equal(t, a, accounts[a].ID)
		assert.Equal(t, ledger.KindUserWallet, accounts[b].Kind)
		return nil
	})
	require.NoError(t, err)
}

func TestLockAccounts_NotFound(t *testing.T) {
	f := setup(t)
	known := f.userAccount(t)

	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.svc.LockAccounts(ctx, tx, []ledger.AccountID{known, ledger.AccountID(uuid.New())})
		return err
	})

	require.ErrorIs(t, err, ledger.ErrAccountNotFound)
}

func TestLockAccounts_BlocksConcurrentLocker(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	ctx := context.Background()
	locked := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_ = f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := f.svc.LockAccounts(ctx, tx, []ledger.AccountID{user}); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	short, err := database.Open(ctx, f.db.Pool().Config().ConnString(), database.Options{
		Tx: database.TxSettings{LockTimeout: 100 * time.Millisecond, StatementTimeout: time.Second, MaxRetries: 0},
	})
	require.NoError(t, err)
	defer short.Close()
	err = short.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.svc.LockAccounts(ctx, tx, []ledger.AccountID{user})
		return err
	})
	close(release)

	assert.True(t, errors.Is(err, database.ErrTryAgain), "the second locker waits for the first: %v", err)
}

func TestGetAccountByCode_NotFound(t *testing.T) {
	f := setup(t)

	_, err := f.svc.GetAccountByCode(context.Background(), f.db.Pool(), "NOPE")
	require.ErrorIs(t, err, ledger.ErrAccountNotFound)
	_, err = f.svc.Balance(context.Background(), f.db.Pool(), ledger.AccountID(uuid.New()))
	require.ErrorIs(t, err, ledger.ErrAccountNotFound)
}

func TestSystemAccountIDs_MatchMigrationSeed(t *testing.T) {
	f := setup(t)
	for _, code := range []string{ledger.CodeBankSettlement, ledger.CodeWithdrawalsInTransit, ledger.CodeSuspense} {
		account := f.system(t, code)
		assert.True(t, ledger.SystemAccountIDs[account.ID], "%s (%s) is in the lock-order list", code, account.ID)
	}
	assert.Len(t, ledger.SystemAccountIDs, 3)
}

func TestCreateUserAccount_NilWalletID(t *testing.T) {
	f := setup(t)
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.svc.CreateUserAccount(ctx, tx, uuid.Nil)
		return err
	})
	assert.ErrorIs(t, err, ledger.ErrMissingWalletID)
}

// TestPost_PreLockedAndUnlockedCreditsNoDeadlock runs two transactions on the same user
// without InTx's retries, so a deadlock would surface as an error: one pre-locks the user with
// LockAccounts and then posts, the other posts straight away. Both credit the user from
// BANK_SETTLEMENT, so both touch the user row and the system row (review T003 #1).
func TestPost_PreLockedAndUnlockedCreditsNoDeadlock(t *testing.T) {
	f := setup(t)
	user := f.userAccount(t)
	bank := f.system(t, ledger.CodeBankSettlement).ID
	ctx := context.Background()
	credit := func() ledger.Entry {
		return ledger.Entry{
			TransactionID: uuid.New(),
			Type:          ledger.EntryTopUpSettle,
			Postings: []ledger.Posting{
				{AccountID: bank, Direction: ledger.Debit, Amount: egp(t, 100)},
				{AccountID: user, Direction: ledger.Credit, Amount: egp(t, 100)},
			},
		}
	}

	preLocker, err := f.db.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = preLocker.Rollback(ctx) }()
	_, err = f.svc.LockAccounts(ctx, preLocker, []ledger.AccountID{user})
	require.NoError(t, err)

	// The second transaction posts without pre-locking. With user rows before system rows it
	// waits on the user row and holds nothing the first one needs.
	direct := make(chan error, 1)
	go func() {
		tx, err := f.db.Pool().Begin(ctx)
		if err != nil {
			direct <- err
			return
		}
		if _, err := f.svc.Post(ctx, tx, credit()); err != nil {
			_ = tx.Rollback(ctx)
			direct <- err
			return
		}
		direct <- tx.Commit(ctx)
	}()
	time.Sleep(300 * time.Millisecond) // let the direct poster reach its first lock

	_, err = f.svc.Post(ctx, preLocker, credit())
	require.NoError(t, err, "pre-locked poster must not deadlock")
	require.NoError(t, preLocker.Commit(ctx))
	require.NoError(t, <-direct, "direct poster must not deadlock")

	assert.Equal(t, int64(200), f.balance(t, user))
}

//go:build integration

package transaction_test

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
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

type fixture struct {
	db    *database.DB
	svc   *transaction.Service
	clock *clocktest.Fake
	user  uuid.UUID
	a, b  uuid.UUID // a belongs to user, b to a second user (one wallet per user)
	other uuid.UUID // wallet of another user
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))
	t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })

	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	wallets := wallet.NewService(db, ledger.NewService(db, clk), clk)
	f := fixture{db: db, svc: transaction.NewService(clk), clock: clk}
	// Users come from identity in the app; here plain rows are enough. The schema allows one
	// wallet per user, so wallets a and b belong to two users and "other" to a third.
	newWallet := func(phone string) (userID, walletID uuid.UUID) {
		userID = uuid.New()
		require.NoError(t, db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
				VALUES ($1, $2, 'Test User', 'x', now())`, userID, phone); err != nil {
				return err
			}
			var err error
			walletID, err = wallets.Open(ctx, tx, userID)
			return err
		}))
		return userID, walletID
	}
	f.user, f.a = newWallet("+201000000001")
	_, f.b = newWallet("+201000000002")
	_, f.other = newWallet("+201000000003")
	return f
}

func egp(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(amount, money.EGP)
	require.NoError(t, err)
	return m
}

func (f fixture) create(t *testing.T, n transaction.New) transaction.Transaction {
	t.Helper()
	if n.InitiatorUserID == uuid.Nil {
		n.InitiatorUserID = f.user
	}
	if n.IdempotencyKey == "" {
		n.IdempotencyKey = uuid.NewString()
	}
	var txn transaction.Transaction
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.svc.Create(ctx, tx, n)
		return err
	}))
	return txn
}

func (f fixture) transition(txnID uuid.UUID, from, to transaction.Status, patch transaction.Patch) (transaction.Transaction, error) {
	var txn transaction.Transaction
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		txn, err = f.svc.Transition(ctx, tx, txnID, from, to, patch)
		return err
	})
	return txn, err
}

func (f fixture) topUp(t *testing.T, amount int64) transaction.Transaction {
	return f.create(t, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending, DestinationWalletID: f.a, Amount: egp(t, amount)})
}

func ptr(s string) *string { return &s }

func TestMigration_TransactionsFK(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	migrator, err := database.NewMigrator(f.db.Pool(), "")
	require.NoError(t, err)
	status, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, status.Version, uint(5))

	_, err = f.db.Pool().Exec(ctx, `INSERT INTO journal_entries (id, transaction_id, entry_type, created_at)
		VALUES ($1, $2, 'ADJUSTMENT', now())`, uuid.New(), uuid.New())
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23503", pgErr.Code, "foreign key violation")
	assert.Equal(t, "journal_entries_transaction_id_fkey", pgErr.ConstraintName)
}

func TestCreate_ByType(t *testing.T) {
	f := setup(t)
	snapshot := &transaction.BankAccountSnapshot{IBAN: "EG890003000100000000123450000", HolderName: "Ahmed Ali"}

	topUp := f.create(t, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending,
		DestinationWalletID: f.a, Amount: egp(t, 5000), BankAccount: snapshot})
	assert.Equal(t, transaction.StatusPending, topUp.Status)
	assert.Nil(t, topUp.CompletedAt)
	assert.Equal(t, snapshot, topUp.BankAccount)
	assert.Equal(t, uuid.Nil, topUp.SourceWalletID)

	withdrawal := f.create(t, transaction.New{Type: transaction.TypeWithdrawal, Status: transaction.StatusPending,
		SourceWalletID: f.a, Amount: egp(t, 700)})
	assert.Equal(t, transaction.StatusPending, withdrawal.Status)
	assert.Nil(t, withdrawal.BankAccount)

	p2p := f.create(t, transaction.New{Type: transaction.TypeP2P, Status: transaction.StatusCompleted,
		SourceWalletID: f.a, DestinationWalletID: f.b, Amount: egp(t, 300), Note: "lunch"})
	assert.Equal(t, transaction.StatusCompleted, p2p.Status)
	require.NotNil(t, p2p.CompletedAt)
	assert.True(t, p2p.CompletedAt.Equal(f.clock.Now()))
	assert.Equal(t, "lunch", p2p.Note)

	got, err := f.svc.Get(context.Background(), f.db.Pool(), p2p.ID)
	require.NoError(t, err)
	assert.Equal(t, p2p, got, "what Create returns equals what is stored")

	err = f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.svc.Create(ctx, tx, transaction.New{Type: transaction.TypeP2P, Status: transaction.StatusPending,
			InitiatorUserID: f.user, SourceWalletID: f.a, DestinationWalletID: f.b, Amount: egp(t, 1), IdempotencyKey: "x"})
		return err
	})
	assert.ErrorIs(t, err, transaction.ErrInvalidInitialStatus)
}

func TestCreate_DBChecksBackTheRules(t *testing.T) {
	f := setup(t)
	_, err := f.db.Pool().Exec(context.Background(), `INSERT INTO transactions
		(id, type, status, initiator_user_id, source_wallet_id, destination_wallet_id, amount, idempotency_key, created_at, updated_at)
		VALUES ($1, 'P2P', 'COMPLETED', $2, $3, $3, 1, 'k', now(), now())`, uuid.New(), f.user, f.a)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "transactions_wallets_by_type", pgErr.ConstraintName, "P2P to the same wallet")
}

func TestCreate_DuplicateIdempotencyKey(t *testing.T) {
	f := setup(t)
	n := transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending, DestinationWalletID: f.a, Amount: egp(t, 100), IdempotencyKey: "same-key"}
	f.create(t, n)

	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		n.InitiatorUserID = f.user
		_, err := f.svc.Create(ctx, tx, n)
		return err
	})
	assert.ErrorIs(t, err, transaction.ErrDuplicateIdempotencyKey)

	var otherUser uuid.UUID
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT user_id FROM wallets WHERE id = $1`, f.other).Scan(&otherUser))
	n.InitiatorUserID, n.DestinationWalletID = otherUser, f.other
	f.create(t, n) // a different user may use the same key
}

func TestTransition_Allowed(t *testing.T) {
	f := setup(t)
	paths := [][]transaction.Status{
		{transaction.StatusPending, transaction.StatusProcessing, transaction.StatusCompleted},
		{transaction.StatusPending, transaction.StatusProcessing, transaction.StatusFailed},
		{transaction.StatusPending, transaction.StatusFailed},
	}
	for _, path := range paths {
		txn := f.topUp(t, 100)
		previous := txn.UpdatedAt
		for i := 1; i < len(path); i++ {
			f.clock.Advance(time.Minute)
			moved, err := f.transition(txn.ID, path[i-1], path[i], transaction.Patch{})
			require.NoError(t, err, "%s → %s", path[i-1], path[i])
			assert.Equal(t, path[i], moved.Status)
			assert.True(t, moved.UpdatedAt.After(previous), "updated_at moves")
			if path[i].Terminal() {
				require.NotNil(t, moved.CompletedAt)
				assert.True(t, moved.CompletedAt.Equal(f.clock.Now()))
			} else {
				assert.Nil(t, moved.CompletedAt)
			}
			previous = moved.UpdatedAt
		}
	}
}

func TestTransition_PatchFields(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)

	processing, err := f.transition(txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: ptr("BANK-1")})
	require.NoError(t, err)
	assert.Equal(t, "BANK-1", processing.BankReference)

	failed, err := f.transition(txn.ID, transaction.StatusProcessing, transaction.StatusFailed,
		transaction.Patch{FailureCode: ptr("INSUFFICIENT_FUNDS"), FailureMessage: ptr("Bank declined.")})
	require.NoError(t, err)
	assert.Equal(t, "BANK-1", failed.BankReference, "a nil patch field keeps the value")
	assert.Equal(t, "INSUFFICIENT_FUNDS", failed.FailureCode)
	assert.Equal(t, "Bank declined.", failed.FailureMessage)
}

func TestTransition_NotAllowed(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)
	for _, pair := range [][2]transaction.Status{
		{transaction.StatusCompleted, transaction.StatusFailed},
		{transaction.StatusFailed, transaction.StatusCompleted},
		{transaction.StatusPending, transaction.StatusCompleted},
		{transaction.StatusProcessing, transaction.StatusPending},
		{transaction.StatusPending, transaction.StatusPending},
	} {
		_, err := f.transition(txn.ID, pair[0], pair[1], transaction.Patch{})
		assert.ErrorIs(t, err, transaction.ErrInvalidTransition, "%s → %s", pair[0], pair[1])
	}
}

func TestTransition_StatusConflict(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)
	_, err := f.transition(txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{})
	require.NoError(t, err)
	before, err := f.svc.Get(context.Background(), f.db.Pool(), txn.ID)
	require.NoError(t, err)

	_, err = f.transition(txn.ID, transaction.StatusPending, transaction.StatusFailed, transaction.Patch{FailureCode: ptr("X")})
	require.ErrorIs(t, err, transaction.ErrStatusConflict)
	var conflict *transaction.StatusConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, transaction.StatusProcessing, conflict.Current)

	after, err := f.svc.Get(context.Background(), f.db.Pool(), txn.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "no change")

	_, err = f.transition(uuid.New(), transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{})
	assert.ErrorIs(t, err, transaction.ErrNotFound)
}

func TestTransition_Concurrent(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.transition(txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	ok, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, transaction.ErrStatusConflict):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, 1, conflicts)
}

func TestTransition_DuplicateBankReference(t *testing.T) {
	f := setup(t)
	first, second := f.topUp(t, 100), f.topUp(t, 200)
	_, err := f.transition(first.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: ptr("BANK-1")})
	require.NoError(t, err)

	_, err = f.transition(second.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: ptr("BANK-1")})
	assert.ErrorIs(t, err, transaction.ErrDuplicateBankReference)
}

func TestFindByBankRef(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)
	_, err := f.transition(txn.ID, transaction.StatusPending, transaction.StatusProcessing, transaction.Patch{BankReference: ptr("BANK-7")})
	require.NoError(t, err)

	found, err := f.svc.FindByBankRef(context.Background(), f.db.Pool(), "BANK-7")
	require.NoError(t, err)
	assert.Equal(t, txn.ID, found.ID)
	_, err = f.svc.FindByBankRef(context.Background(), f.db.Pool(), "NOPE")
	assert.ErrorIs(t, err, transaction.ErrNotFound)
}

func TestGetForUpdate(t *testing.T) {
	f := setup(t)
	txn := f.topUp(t, 100)
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		locked, err := f.svc.GetForUpdate(ctx, tx, txn.ID)
		require.NoError(t, err)
		assert.Equal(t, txn.ID, locked.ID)
		_, err = f.svc.GetForUpdate(ctx, tx, uuid.New())
		assert.ErrorIs(t, err, transaction.ErrNotFound)
		return nil
	}))
}

func TestSumOutgoingForDay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// 2026-11-10 in Cairo is UTC+2 (winter): [2026-11-09T22:00Z, 2026-11-10T22:00Z).
	dayStart, dayEnd := clock.CairoDay(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 11, 9, 22, 0, 0, 0, time.UTC), dayStart)

	at := func(ts time.Time) { f.clock.Set(ts) }
	withdraw := func(amount int64, wallet uuid.UUID) transaction.Transaction {
		return f.create(t, transaction.New{Type: transaction.TypeWithdrawal, Status: transaction.StatusPending, SourceWalletID: wallet, Amount: egp(t, amount)})
	}
	p2p := func(amount int64, from, to uuid.UUID) {
		f.create(t, transaction.New{Type: transaction.TypeP2P, Status: transaction.StatusCompleted, SourceWalletID: from, DestinationWalletID: to, Amount: egp(t, amount)})
	}
	move := func(txn transaction.Transaction, path ...transaction.Status) {
		from := txn.Status
		for _, to := range path {
			_, err := f.transition(txn.ID, from, to, transaction.Patch{})
			require.NoError(t, err)
			from = to
		}
	}

	at(dayStart.Add(-time.Second)) // 21:59:59Z: the previous Cairo day
	withdraw(100_000, f.a)
	p2p(100_000, f.a, f.b)

	at(dayStart)     // 22:00:00Z: first instant of the day
	withdraw(1, f.a) // PENDING counts
	move(withdraw(10, f.a), transaction.StatusProcessing)
	move(withdraw(100, f.a), transaction.StatusProcessing, transaction.StatusCompleted)
	move(withdraw(5_000, f.a), transaction.StatusFailed) // FAILED does not count
	p2p(1_000, f.a, f.b)                                 // outgoing P2P counts
	p2p(7_000, f.b, f.a)                                 // incoming P2P does not
	withdraw(9_000, f.other)                             // another wallet does not
	f.topUp(t, 3_000)                                    // top-ups are not outgoing

	at(dayEnd) // first instant of the next day
	withdraw(100_000, f.a)

	sum, err := f.svc.SumOutgoingForDay(ctx, f.db.Pool(), f.a, dayStart, dayEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(1+10+100+1_000), sum)

	empty, err := f.svc.SumOutgoingForDay(ctx, f.db.Pool(), uuid.New(), dayStart, dayEnd)
	require.NoError(t, err)
	assert.Zero(t, empty, "0 when there is nothing")
}

func TestSumPendingTopUps(t *testing.T) {
	f := setup(t)
	f.topUp(t, 100)                                                                                                                               // PENDING
	processing := f.topUp(t, 200)                                                                                                                 // → PROCESSING
	completed := f.topUp(t, 400)                                                                                                                  // → COMPLETED
	failed := f.topUp(t, 800)                                                                                                                     // → FAILED
	f.create(t, transaction.New{Type: transaction.TypeTopUp, Status: transaction.StatusPending, DestinationWalletID: f.b, Amount: egp(t, 1_600)}) // other wallet
	for txn, path := range map[uuid.UUID][]transaction.Status{
		processing.ID: {transaction.StatusProcessing},
		completed.ID:  {transaction.StatusProcessing, transaction.StatusCompleted},
		failed.ID:     {transaction.StatusFailed},
	} {
		from := transaction.StatusPending
		for _, to := range path {
			_, err := f.transition(txn, from, to, transaction.Patch{})
			require.NoError(t, err)
			from = to
		}
	}

	sum, err := f.svc.SumPendingTopUps(context.Background(), f.db.Pool(), f.a)
	require.NoError(t, err)
	assert.Equal(t, int64(100+200), sum)
}

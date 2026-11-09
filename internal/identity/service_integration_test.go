//go:build integration

package identity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/ledger/ledgertest"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

const secret = "integration-secret-at-least-32-bytes-long"

type fixture struct {
	db      *database.DB
	wallets *wallet.Service
	clock   *clocktest.Fake
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

	clk := clocktest.New(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	return fixture{db: db, wallets: wallet.NewService(db, ledger.NewService(db, clk), clk), clock: clk}
}

func (f fixture) service(t *testing.T, opener identity.WalletOpener) *identity.Service {
	t.Helper()
	svc, err := identity.NewService(f.db, opener, f.clock, identity.Config{JWTSecret: secret, JWTTTL: 15 * time.Minute, BcryptCost: bcrypt.MinCost})
	require.NoError(t, err)
	return svc
}

func input(phone string) identity.RegisterInput {
	return identity.RegisterInput{Phone: phone, FullName: "  Ahmed Mohamed Ali ", Password: "s3cret-pass"}
}

func TestRegister_CreatesUserWalletAccount(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	reg, err := f.service(t, f.wallets).Register(ctx, input("+201001234567"))
	require.NoError(t, err)
	assert.Equal(t, "Ahmed Mohamed Ali", reg.User.FullName, "name is stored trimmed")
	assert.Equal(t, identity.StatusActive, reg.User.Status)

	var hash, status string
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT password_hash, status FROM users WHERE id = $1`, reg.User.ID).Scan(&hash, &status))
	assert.NotEqual(t, "s3cret-pass", hash)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("s3cret-pass")), "bcrypt hash of the password")
	assert.Equal(t, "ACTIVE", status)

	w, err := f.wallets.GetByUserID(ctx, f.db.Pool(), reg.User.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.WalletID, w.ID)
	assert.Equal(t, wallet.StatusActive, w.Status)
	assert.Equal(t, "EGP", string(w.Currency))

	var code, kind string
	var balance int64
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT code, kind, balance FROM ledger_accounts WHERE id = $1`, uuid.UUID(w.LedgerAccountID)).Scan(&code, &kind, &balance))
	assert.Equal(t, "USER_WALLET:"+reg.WalletID.String(), code)
	assert.Equal(t, "USER_WALLET", kind)
	assert.Zero(t, balance)
}

// failingOpener opens the wallet for real (user, wallet and ledger account are inserted)
// and then fails, so the test can check that nothing of it is committed.
type failingOpener struct{ real *wallet.Service }

var errForced = errors.New("forced failure after the wallet insert")

func (o failingOpener) Open(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	if _, err := o.real.Open(ctx, tx, userID); err != nil {
		return uuid.Nil, err
	}
	return uuid.Nil, errForced
}

func TestRegister_RollsBackOnFailure(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	_, err := f.service(t, failingOpener{real: f.wallets}).Register(ctx, input("+201001234567"))
	require.ErrorIs(t, err, errForced)

	var users, wallets, accounts int
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users), (SELECT count(*) FROM wallets),
		(SELECT count(*) FROM ledger_accounts WHERE kind = 'USER_WALLET')`).Scan(&users, &wallets, &accounts))
	assert.Zero(t, users)
	assert.Zero(t, wallets)
	assert.Zero(t, accounts)
}

func TestRegister_DuplicatePhone(t *testing.T) {
	f := setup(t)
	svc := f.service(t, f.wallets)
	_, err := svc.Register(context.Background(), input("+201001234567"))
	require.NoError(t, err)

	_, err = svc.Register(context.Background(), input("+201001234567"))
	assert.ErrorIs(t, err, identity.ErrPhoneTaken)
}

func TestRegister_ParallelSamePhone(t *testing.T) {
	f := setup(t)
	svc := f.service(t, f.wallets)
	const n = 10

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Register(context.Background(), input("+201001234567"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	ok, taken := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, identity.ErrPhoneTaken):
			taken++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, n-1, taken)
}

func TestLogin_Service(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := f.service(t, f.wallets)
	reg, err := svc.Register(ctx, input("+201001234567"))
	require.NoError(t, err)

	token, err := svc.Login(ctx, "+201001234567", "s3cret-pass")
	require.NoError(t, err)
	userID, err := svc.Authenticate(ctx, token.Token)
	require.NoError(t, err)
	assert.Equal(t, reg.User.ID, userID)

	_, err = svc.Login(ctx, "+201001234567", "wrong-pass")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials)
	_, err = svc.Login(ctx, "+201009999999", "s3cret-pass")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials)
	_, err = svc.Login(ctx, "not-a-phone", "s3cret-pass")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials)

	_, err = f.db.Pool().Exec(ctx, `UPDATE users SET status = 'DISABLED' WHERE id = $1`, reg.User.ID)
	require.NoError(t, err)
	_, err = svc.Login(ctx, "+201001234567", "wrong-pass")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials, "a disabled user with a wrong password learns nothing")
	_, err = svc.Login(ctx, "+201001234567", "s3cret-pass")
	assert.ErrorIs(t, err, identity.ErrUserDisabled)
	_, err = svc.Authenticate(ctx, token.Token)
	assert.ErrorIs(t, err, identity.ErrUserDisabled, "an existing token stops working")
}

func TestGetUser(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := f.service(t, f.wallets)
	reg, err := svc.Register(ctx, input("+201001234567"))
	require.NoError(t, err)

	byID, err := svc.GetUser(ctx, reg.User.ID)
	require.NoError(t, err)
	byPhone, err := svc.GetUserByPhone(ctx, "+201001234567")
	require.NoError(t, err)
	assert.Equal(t, byID, byPhone)

	_, err = svc.GetUser(ctx, uuid.New())
	assert.ErrorIs(t, err, identity.ErrUserNotFound)
	_, err = svc.GetUserByPhone(ctx, "+201009999999")
	assert.ErrorIs(t, err, identity.ErrUserNotFound)
}

func TestLogin_PasswordOver72BytesRejected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := f.service(t, f.wallets)
	password72 := strings.Repeat("p", 72)
	in := input("+201001234567")
	in.Password = password72
	_, err := svc.Register(ctx, in)
	require.NoError(t, err)

	_, err = svc.Login(ctx, "+201001234567", password72)
	require.NoError(t, err, "the exact 72-byte password works")
	_, err = svc.Login(ctx, "+201001234567", password72+"anything")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials, "bcrypt would ignore the suffix; login must not")
}

func TestRegister_CreatedAtMatchesStoredValue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.clock.Set(time.Date(2026, 11, 6, 10, 0, 0, 123456789, time.UTC))
	svc := f.service(t, f.wallets)

	reg, err := svc.Register(ctx, input("+201001234567"))
	require.NoError(t, err)
	stored, err := svc.GetUser(ctx, reg.User.ID)
	require.NoError(t, err)
	assert.True(t, reg.User.CreatedAt.Equal(stored.CreatedAt), "201 body %s vs stored %s", reg.User.CreatedAt, stored.CreatedAt)
}

func TestGetBankAccount_NotFoundThenFound(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := f.service(t, f.wallets)
	reg, err := svc.Register(ctx, input("+201001234567"))
	require.NoError(t, err)

	_, err = svc.GetBankAccount(ctx, f.db.Pool(), reg.User.ID)
	require.ErrorIs(t, err, identity.ErrBankAccountNotFound)

	linked, err := svc.LinkBankAccount(ctx, reg.User.ID, "eg89 0003 0001 0000 0000 1234 5000 0", "  Ahmed Ali ")
	require.NoError(t, err)
	assert.Equal(t, "EG890003000100000000123450000", linked.IBAN)
	assert.Equal(t, "Ahmed Ali", linked.HolderName)

	err = f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		got, err := svc.GetBankAccount(ctx, tx, reg.User.ID)
		require.NoError(t, err)
		assert.Equal(t, linked, got, "readable inside a unit of work too")
		return nil
	})
	require.NoError(t, err)
}

func TestLinkBankAccount_ValidationOrder(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := f.service(t, f.wallets)
	reg, err := svc.Register(ctx, input("+201001234567"))
	require.NoError(t, err)

	_, err = svc.LinkBankAccount(ctx, reg.User.ID, "EG00", " ")
	assert.ErrorIs(t, err, identity.ErrInvalidIBAN, "IBAN is checked before the holder name")
	_, err = svc.LinkBankAccount(ctx, reg.User.ID, "EG890003000100000000123450000", " A ")
	assert.ErrorIs(t, err, identity.ErrInvalidHolderName)
	_, err = svc.GetBankAccount(ctx, f.db.Pool(), reg.User.ID)
	assert.ErrorIs(t, err, identity.ErrBankAccountNotFound, "nothing stored on a validation error")
}

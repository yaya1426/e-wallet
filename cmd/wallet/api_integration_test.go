//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/ledger/ledgertest"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/config"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/logger"
	"github.com/yaya1426/e-wallet/internal/platform/metrics"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
)

type apiFixture struct {
	db      *database.DB
	server  *httptest.Server
	clock   clock.Clock
	ledger  *ledger.Service
	txns    *transaction.Service
	retries *txRetries
}

// txRetries is a database.TxObserver that counts InTx retries by reason, so tests can prove
// that no deadlock happened (InTx would otherwise retry it silently).
type txRetries struct {
	mu       sync.Mutex
	byReason map[string]int
}

func (r *txRetries) ObserveTxRetry(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byReason[reason]++
}

func (r *txRetries) ObserveTxTryAgain() {}

func (r *txRetries) count(reason string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byReason[reason]
}

// newAPIFixture serves the real wiring (newAPI + newHandler) against a migrated database,
// with the default configuration (limits included) and the system clock.
func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	return newAPIFixtureWith(t, clock.System{}, nil)
}

// newAPIFixtureWith is newAPIFixture with a given clock and extra environment variables
// (e.g. LIMIT_* overrides).
func newAPIFixtureWith(t *testing.T, clk clock.Clock, extraEnv map[string]string) apiFixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, stderr := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	retries := &txRetries{byReason: map[string]int{}}
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings(), Observer: retries})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })

	environ := map[string]string{"DATABASE_URL": url, "JWT_SECRET": "integration-secret-at-least-32-bytes-long", "BANK_BASE_URL": testBankURL,
		"BANK_WEBHOOK_SECRET": testWebhookSecret}
	for k, v := range extraEnv {
		environ[k] = v
	}
	cfg, err := config.LoadWallet(environ)
	require.NoError(t, err)
	require.NoError(t, cfg.ValidateServe())

	log := logger.New(io.Discard, slog.LevelInfo)
	a, err := newAPI(db, cfg, clk, log, nil, bcrypt.MinCost)
	require.NoError(t, err)
	srv := httptest.NewServer(newHandler(log, metrics.New(), nil, a.routes))
	t.Cleanup(srv.Close)
	return apiFixture{db: db, server: srv, clock: clk, ledger: ledger.NewService(db, clk), txns: transaction.NewService(clk), retries: retries}
}

// fund credits the wallet as a completed top-up would: a TOPUP transaction moved to
// COMPLETED and its TOPUP_SETTLE entry (Dr BANK_SETTLEMENT / Cr user).
func (f apiFixture) fund(t *testing.T, userID, walletID string, amountMinor int64) {
	t.Helper()
	ctx := context.Background()
	bank, err := f.ledger.GetAccountByCode(ctx, f.db.Pool(), ledger.CodeBankSettlement)
	require.NoError(t, err)
	user, err := f.ledger.GetAccountByCode(ctx, f.db.Pool(), "USER_WALLET:"+walletID)
	require.NoError(t, err)
	amount, err := money.NewPositive(amountMinor, money.EGP)
	require.NoError(t, err)
	require.NoError(t, f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		txn, err := f.txns.Create(ctx, tx, transaction.New{
			Type: transaction.TypeTopUp, Status: transaction.StatusPending,
			InitiatorUserID: uuid.MustParse(userID), DestinationWalletID: uuid.MustParse(walletID),
			Amount: amount, IdempotencyKey: "fund-" + uuid.NewString(),
		})
		if err != nil {
			return err
		}
		for _, step := range [][2]transaction.Status{
			{transaction.StatusPending, transaction.StatusProcessing},
			{transaction.StatusProcessing, transaction.StatusCompleted},
		} {
			if _, err := f.txns.Transition(ctx, tx, txn.ID, step[0], step[1], transaction.Patch{}); err != nil {
				return err
			}
		}
		_, err = f.ledger.Post(ctx, tx, ledger.Entry{
			TransactionID: txn.ID,
			Type:          ledger.EntryTopUpSettle,
			Postings: []ledger.Posting{
				{AccountID: bank.ID, Direction: ledger.Debit, Amount: amount},
				{AccountID: user.ID, Direction: ledger.Credit, Amount: amount},
			},
		})
		return err
	}))
}

// balance reads the wallet's available balance through the API.
func (f apiFixture) balance(t *testing.T, token string) int64 {
	t.Helper()
	r := f.call(t, http.MethodGet, "/v1/me/wallet", token, "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	return int64(r.body["available_balance_minor"].(float64))
}

type httpResult struct {
	status int
	body   map[string]any
}

func (f apiFixture) call(t *testing.T, method, path, token, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var decoded map[string]any
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return httpResult{status: resp.StatusCode, body: decoded}
}

func errorCode(r httpResult) string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

const registerBody = `{"phone":"+201001234567","full_name":"Ahmed Mohamed Ali","password":"s3cret-pass"}`

func (f apiFixture) registerAndLogin(t *testing.T) (userID, walletID, token string) {
	t.Helper()
	return f.registerAndLoginAs(t, "+201001234567")
}

func (f apiFixture) registerAndLoginAs(t *testing.T, phone string) (userID, walletID, token string) {
	t.Helper()
	reg := f.call(t, http.MethodPost, "/v1/auth/register", "",
		`{"phone":"`+phone+`","full_name":"Ahmed Mohamed Ali","password":"s3cret-pass"}`)
	require.Equal(t, http.StatusCreated, reg.status, reg.body)
	login := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"`+phone+`","password":"s3cret-pass"}`)
	require.Equal(t, http.StatusOK, login.status, login.body)
	return reg.body["user_id"].(string), reg.body["wallet_id"].(string), login.body["access_token"].(string)
}

func TestAPI_RegisterLoginWallet(t *testing.T) {
	f := newAPIFixture(t)

	reg := f.call(t, http.MethodPost, "/v1/auth/register", "", registerBody)
	require.Equal(t, http.StatusCreated, reg.status, reg.body)
	assert.Equal(t, "+201001234567", reg.body["phone"])
	assert.Equal(t, "Ahmed Mohamed Ali", reg.body["full_name"])
	for _, key := range []string{"user_id", "wallet_id"} {
		_, err := uuid.Parse(reg.body[key].(string))
		assert.NoError(t, err, key)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, reg.body["created_at"].(string))
	require.NoError(t, err)
	assert.Equal(t, time.UTC, createdAt.Location())
	assert.NotContains(t, reg.body, "access_token", "register returns no token")

	login := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"+201001234567","password":"s3cret-pass"}`)
	require.Equal(t, http.StatusOK, login.status, login.body)
	assert.Equal(t, "Bearer", login.body["token_type"])
	assert.EqualValues(t, 900, login.body["expires_in"])

	wallet := f.call(t, http.MethodGet, "/v1/me/wallet", login.body["access_token"].(string), "")
	require.Equal(t, http.StatusOK, wallet.status, wallet.body)
	assert.Equal(t, reg.body["wallet_id"], wallet.body["wallet_id"])
	assert.Equal(t, "EGP", wallet.body["currency"])
	assert.EqualValues(t, 0, wallet.body["available_balance_minor"])
	assert.Equal(t, "ACTIVE", wallet.body["status"])
}

func TestAPI_WalletShowsLedgerBalance(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	userID, walletID, token := f.registerAndLogin(t)

	ledgerSvc := ledger.NewService(f.db, clock.System{})
	txnSvc := transaction.NewService(clock.System{})
	bank, err := ledgerSvc.GetAccountByCode(ctx, f.db.Pool(), ledger.CodeBankSettlement)
	require.NoError(t, err)
	user, err := ledgerSvc.GetAccountByCode(ctx, f.db.Pool(), "USER_WALLET:"+walletID)
	require.NoError(t, err)
	amount, err := money.NewPositive(15075, money.EGP)
	require.NoError(t, err)
	require.NoError(t, f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Every entry belongs to a transaction (T006 FK): a top-up that the bank confirmed.
		txn, err := txnSvc.Create(ctx, tx, transaction.New{
			Type: transaction.TypeTopUp, Status: transaction.StatusPending,
			InitiatorUserID: uuid.MustParse(userID), DestinationWalletID: uuid.MustParse(walletID),
			Amount: amount, IdempotencyKey: "test-topup",
		})
		if err != nil {
			return err
		}
		_, err = ledgerSvc.Post(ctx, tx, ledger.Entry{
			TransactionID: txn.ID,
			Type:          ledger.EntryTopUpSettle,
			Postings: []ledger.Posting{
				{AccountID: bank.ID, Direction: ledger.Debit, Amount: amount},
				{AccountID: user.ID, Direction: ledger.Credit, Amount: amount},
			},
		})
		return err
	}))

	wallet := f.call(t, http.MethodGet, "/v1/me/wallet", token, "")
	require.Equal(t, http.StatusOK, wallet.status)
	assert.EqualValues(t, 15075, wallet.body["available_balance_minor"])
}

func TestAPI_RegisterErrors(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.call(t, http.MethodPost, "/v1/auth/register", "", registerBody)
	require.Equal(t, http.StatusCreated, reg.status)

	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"unknown field", `{"phone":"+201001234568","full_name":"Ali Ali","password":"s3cret-pass","admin":true}`, 400, "MALFORMED_REQUEST"},
		{"array body", `[]`, 400, "MALFORMED_REQUEST"},
		{"trailing data", registerBody + `{}`, 400, "MALFORMED_REQUEST"},
		{"phone as number", `{"phone":201001234568,"full_name":"Ali Ali","password":"s3cret-pass"}`, 400, "MALFORMED_REQUEST"},
		{"65 KB body", `{"phone":"` + strings.Repeat("1", 65<<10) + `"}`, 413, "REQUEST_TOO_LARGE"},
		{"local phone format", `{"phone":"01001234568","full_name":"Ali Ali","password":"s3cret-pass"}`, 400, "INVALID_PHONE"},
		{"short password", `{"phone":"+201001234568","full_name":"Ali Ali","password":"short"}`, 400, "INVALID_PASSWORD"},
		{"short name", `{"phone":"+201001234568","full_name":" A ","password":"s3cret-pass"}`, 400, "INVALID_NAME"},
		{"duplicate phone", registerBody, 409, "PHONE_ALREADY_REGISTERED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.call(t, http.MethodPost, "/v1/auth/register", "", tt.body)
			assert.Equal(t, tt.status, r.status, r.body)
			assert.Equal(t, tt.code, errorCode(r))
			e := r.body["error"].(map[string]any)
			assert.NotEmpty(t, e["message"])
			assert.NotEmpty(t, e["request_id"])
		})
	}
}

func TestAPI_LoginErrors(t *testing.T) {
	f := newAPIFixture(t)
	userID, _, token := f.registerAndLogin(t)

	wrong := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"+201001234567","password":"wrong-pass"}`)
	unknown := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"+201009999999","password":"s3cret-pass"}`)
	assert.Equal(t, http.StatusUnauthorized, wrong.status)
	assert.Equal(t, "INVALID_CREDENTIALS", errorCode(wrong))
	assert.Equal(t, http.StatusUnauthorized, unknown.status)
	assert.Equal(t, errorCode(wrong), errorCode(unknown), "unknown phone and wrong password look the same")

	_, err := f.db.Pool().Exec(context.Background(), `UPDATE users SET status = 'DISABLED' WHERE id = $1`, userID)
	require.NoError(t, err)
	disabled := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"+201001234567","password":"s3cret-pass"}`)
	assert.Equal(t, http.StatusForbidden, disabled.status)
	assert.Equal(t, "USER_DISABLED", errorCode(disabled))

	wallet := f.call(t, http.MethodGet, "/v1/me/wallet", token, "")
	assert.Equal(t, http.StatusForbidden, wallet.status)
	assert.Equal(t, "USER_DISABLED", errorCode(wallet))
}

func TestAPI_LoginRateLimit(t *testing.T) {
	f := newAPIFixture(t)
	body := `{"phone":"+201001234567","password":"wrong-pass"}`
	for i := range 10 {
		r := f.call(t, http.MethodPost, "/v1/auth/login", "", body)
		require.Equal(t, http.StatusUnauthorized, r.status, "attempt %d", i+1)
	}
	r := f.call(t, http.MethodPost, "/v1/auth/login", "", body)
	assert.Equal(t, http.StatusTooManyRequests, r.status)
	assert.Equal(t, "TOO_MANY_REQUESTS", errorCode(r))
}

func TestAPI_WalletRequiresAuth(t *testing.T) {
	f := newAPIFixture(t)
	for name, token := range map[string]string{"no token": "", "garbage": "garbage"} {
		r := f.call(t, http.MethodGet, "/v1/me/wallet", token, "")
		assert.Equal(t, http.StatusUnauthorized, r.status, name)
		assert.Equal(t, "UNAUTHENTICATED", errorCode(r), name)
	}
}

func TestPutBankAccount_CreateThenReplace(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	userID, _, token := f.registerAndLogin(t)

	first := f.call(t, http.MethodPut, "/v1/me/bank-account", token,
		`{"iban":"eg89 0003 0001 0000 0000 1234 5000 0","holder_name":" Ahmed Mohamed Ali "}`)
	require.Equal(t, http.StatusOK, first.status, first.body)
	assert.Equal(t, "EG890003000100000000123450000", first.body["iban"])
	assert.Equal(t, "Ahmed Mohamed Ali", first.body["holder_name"])

	var rowID uuid.UUID
	var createdAt, updatedAt time.Time
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT id, created_at, updated_at FROM bank_accounts WHERE user_id = $1`, userID).Scan(&rowID, &createdAt, &updatedAt))

	time.Sleep(5 * time.Millisecond) // updated_at must move forward
	second := f.call(t, http.MethodPut, "/v1/me/bank-account", token,
		`{"iban":"EG620003000100000000123450001","holder_name":"Ahmed Ali"}`)
	require.Equal(t, http.StatusOK, second.status, second.body)
	assert.Equal(t, "EG620003000100000000123450001", second.body["iban"])

	var rows int
	var rowID2 uuid.UUID
	var createdAt2, updatedAt2 time.Time
	require.NoError(t, f.db.Pool().QueryRow(ctx, `SELECT count(*) OVER (), id, created_at, updated_at FROM bank_accounts WHERE user_id = $1`, userID).Scan(&rows, &rowID2, &createdAt2, &updatedAt2))
	assert.Equal(t, 1, rows, "still one row per user")
	assert.Equal(t, rowID, rowID2, "same id")
	assert.True(t, createdAt.Equal(createdAt2), "created_at unchanged")
	assert.True(t, updatedAt2.After(updatedAt), "updated_at newer")

	got := f.call(t, http.MethodGet, "/v1/me/bank-account", token, "")
	require.Equal(t, http.StatusOK, got.status)
	assert.Equal(t, second.body, got.body)
}

func TestGetBankAccount_NotFoundThenFound_HTTP(t *testing.T) {
	f := newAPIFixture(t)
	_, _, token := f.registerAndLogin(t)

	before := f.call(t, http.MethodGet, "/v1/me/bank-account", token, "")
	assert.Equal(t, http.StatusNotFound, before.status)
	assert.Equal(t, "BANK_ACCOUNT_NOT_FOUND", errorCode(before))

	put := f.call(t, http.MethodPut, "/v1/me/bank-account", token, `{"iban":"EG350003000100000000123450002","holder_name":"Ahmed Ali"}`)
	require.Equal(t, http.StatusOK, put.status)
	after := f.call(t, http.MethodGet, "/v1/me/bank-account", token, "")
	assert.Equal(t, http.StatusOK, after.status)
}

func TestBankAccount_IsolatedPerUser(t *testing.T) {
	f := newAPIFixture(t)
	_, _, tokenA := f.registerAndLoginAs(t, "+201001234567")
	_, _, tokenB := f.registerAndLoginAs(t, "+201001234568")

	require.Equal(t, http.StatusOK, f.call(t, http.MethodPut, "/v1/me/bank-account", tokenA,
		`{"iban":"EG890003000100000000123450000","holder_name":"User A"}`).status)

	b := f.call(t, http.MethodGet, "/v1/me/bank-account", tokenB, "")
	assert.Equal(t, http.StatusNotFound, b.status, "B does not see A's account")

	require.Equal(t, http.StatusOK, f.call(t, http.MethodPut, "/v1/me/bank-account", tokenB,
		`{"iban":"EG620003000100000000123450001","holder_name":"User B"}`).status)
	a := f.call(t, http.MethodGet, "/v1/me/bank-account", tokenA, "")
	require.Equal(t, http.StatusOK, a.status)
	assert.Equal(t, "EG890003000100000000123450000", a.body["iban"], "B's PUT did not change A's account")
	assert.Equal(t, "User A", a.body["holder_name"])
}

func TestBankAccount_Errors(t *testing.T) {
	f := newAPIFixture(t)
	_, _, token := f.registerAndLogin(t)
	tests := []struct {
		name, token, body string
		status            int
		code              string
	}{
		{"no token", "", `{"iban":"EG890003000100000000123450000","holder_name":"Ahmed Ali"}`, 401, "UNAUTHENTICATED"},
		{"unknown field", token, `{"iban":"EG890003000100000000123450000","holder_name":"Ahmed Ali","bank":"x"}`, 400, "MALFORMED_REQUEST"},
		{"bad checksum", token, `{"iban":"EG880003000100000000123450000","holder_name":"Ahmed Ali"}`, 400, "INVALID_IBAN"},
		{"not Egypt", token, `{"iban":"GB82WEST12345698765432","holder_name":"Ahmed Ali"}`, 400, "INVALID_IBAN"},
		{"bad holder name", token, `{"iban":"EG890003000100000000123450000","holder_name":"A"}`, 400, "INVALID_HOLDER_NAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.call(t, http.MethodPut, "/v1/me/bank-account", tt.token, tt.body)
			assert.Equal(t, tt.status, r.status, r.body)
			assert.Equal(t, tt.code, errorCode(r))
		})
	}
}

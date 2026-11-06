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
)

type apiFixture struct {
	db     *database.DB
	server *httptest.Server
}

// newAPIFixture serves the real wiring (newAPI + newHandler) against a migrated database.
func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	code, _, stderr := runCLI(t, map[string]string{"DATABASE_URL": url}, "migrate", "up")
	require.Equal(t, exitOK, code, stderr)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	t.Cleanup(func() { ledgertest.AssertIntegrity(t, db) })

	log := logger.New(io.Discard, slog.LevelInfo)
	cfg := config.Wallet{JWTSecret: "integration-secret-at-least-32-bytes-long", JWTTTL: 15 * time.Minute}
	a, err := newAPI(db, cfg, clock.System{}, log, bcrypt.MinCost)
	require.NoError(t, err)
	srv := httptest.NewServer(newHandler(log, metrics.New(), nil, a.routes))
	t.Cleanup(srv.Close)
	return apiFixture{db: db, server: srv}
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
	reg := f.call(t, http.MethodPost, "/v1/auth/register", "", registerBody)
	require.Equal(t, http.StatusCreated, reg.status, reg.body)
	login := f.call(t, http.MethodPost, "/v1/auth/login", "", `{"phone":"+201001234567","password":"s3cret-pass"}`)
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
	_, walletID, token := f.registerAndLogin(t)

	ledgerSvc := ledger.NewService(f.db, clock.System{})
	bank, err := ledgerSvc.GetAccountByCode(ctx, f.db.Pool(), ledger.CodeBankSettlement)
	require.NoError(t, err)
	user, err := ledgerSvc.GetAccountByCode(ctx, f.db.Pool(), "USER_WALLET:"+walletID)
	require.NoError(t, err)
	amount, err := money.NewPositive(15075, money.EGP)
	require.NoError(t, err)
	require.NoError(t, f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := ledgerSvc.Post(ctx, tx, ledger.Entry{
			TransactionID: uuid.New(),
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

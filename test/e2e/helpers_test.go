//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Test IBANs (valid mod-97); banksim picks the scenario from the last four digits (T014).
const (
	ibanSuccess  = "EG890003000100000000123450000" // the bank succeeds after the webhook delay
	ibanFailLate = "EG620003000100000000123450001" // accepted, then failed by webhook
	ibanReject   = "EG350003000100000000123450002" // rejected at once (INVALID_ACCOUNT)
)

// finalTimeout bounds waiting for a top-up or withdrawal to become COMPLETED or FAILED. The
// simulator settles after 2 s by default.
const finalTimeout = 15 * time.Second

// apiResponse is one wallet API answer.
type apiResponse struct {
	status   int
	body     map[string]any
	replayed bool
}

func (r apiResponse) errorCode() string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// api calls the wallet. token and key may be empty; body is JSON or empty.
func api(t *testing.T, method, path, token, key, body string) apiResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, walletURL+path, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return apiResponse{status: resp.StatusCode, body: decoded, replayed: resp.Header.Get("Idempotent-Replayed") == "true"}
}

// apiWithHeader calls the wallet with one extra header and no body.
func apiWithHeader(t *testing.T, method, path, header, value string) apiResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, walletURL+path, nil)
	require.NoError(t, err)
	req.Header.Set(header, value)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return apiResponse{status: resp.StatusCode, body: decoded}
}

// loginPolitely logs in; on 429 (the login limit is 30 per client IP per minute, TDR-018) it
// waits for the window to pass and tries again, so a fast suite run from one IP does not fail.
func loginPolitely(t *testing.T, phone, password string) apiResponse {
	t.Helper()
	body := fmt.Sprintf(`{"phone":%q,"password":%q}`, phone, password)
	deadline := time.Now().Add(90 * time.Second)
	for {
		r := api(t, http.MethodPost, "/v1/auth/login", "", "", body)
		if r.status != http.StatusTooManyRequests || time.Now().After(deadline) {
			return r
		}
		time.Sleep(5 * time.Second)
	}
}

// user is a registered, logged-in user.
type user struct {
	phone, token, userID, walletID string
}

// scenario starts an e2e scenario: it waits for the stack and checks the ledger after the
// test (integrity_test.go), so a broken invariant fails the test that broke it.
func scenario(t *testing.T) {
	t.Helper()
	waitReady(t)
	t.Cleanup(func() { checkIntegrity(t) })
}

// newUser registers and logs in a user with a fresh random phone, so tests are independent
// and can run again on the same database.
func newUser(t *testing.T, name string) user {
	t.Helper()
	phone := fmt.Sprintf("+2010%08d", rand.N(100_000_000))
	reg := api(t, http.MethodPost, "/v1/auth/register", "", "",
		fmt.Sprintf(`{"phone":%q,"full_name":%q,"password":"e2e-pass-123"}`, phone, name))
	require.Equal(t, http.StatusCreated, reg.status, reg.body)
	login := loginPolitely(t, phone, "e2e-pass-123")
	require.Equal(t, http.StatusOK, login.status, login.body)
	return user{phone: phone, token: login.body["access_token"].(string),
		userID: reg.body["user_id"].(string), walletID: reg.body["wallet_id"].(string)}
}

// linkBank links (or replaces) the user's bank account.
func (u user) linkBank(t *testing.T, iban string) {
	t.Helper()
	r := api(t, http.MethodPut, "/v1/me/bank-account", u.token, "", fmt.Sprintf(`{"iban":%q,"holder_name":"E2E User"}`, iban))
	require.Equal(t, http.StatusOK, r.status, r.body)
}

func amountBody(minor int64) string {
	return fmt.Sprintf(`{"amount_minor":%d,"currency":"EGP"}`, minor)
}

// request posts a top-up or withdrawal (path /v1/topups or /v1/withdrawals) with key ("" =
// a new key) and requires 202.
func (u user) request(t *testing.T, path string, minor int64, key string) apiResponse {
	t.Helper()
	if key == "" {
		key = uuid.NewString()
	}
	r := api(t, http.MethodPost, path, u.token, key, amountBody(minor))
	require.Equal(t, http.StatusAccepted, r.status, r.body)
	require.Equal(t, "PENDING", r.body["status"])
	return r
}

// waitFinal polls GET /v1/transactions/{id} until COMPLETED or FAILED and returns it.
func (u user) waitFinal(t *testing.T, txnID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(finalTimeout)
	for {
		r := api(t, http.MethodGet, "/v1/transactions/"+txnID, u.token, "", "")
		require.Equal(t, http.StatusOK, r.status, r.body)
		if s := r.body["status"]; s == "COMPLETED" || s == "FAILED" {
			return r.body
		}
		if time.Now().After(deadline) {
			t.Fatalf("transaction %s still %v after %s", txnID, r.body["status"], finalTimeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// fund tops the user up by minor piasters through the real flow (success IBAN) and waits for
// it to complete. The success IBAN stays linked.
func (u user) fund(t *testing.T, minor int64) {
	t.Helper()
	u.linkBank(t, ibanSuccess)
	r := u.request(t, "/v1/topups", minor, "")
	final := u.waitFinal(t, r.body["transaction_id"].(string))
	require.Equal(t, "COMPLETED", final["status"], final)
}

func (u user) balance(t *testing.T) int64 {
	t.Helper()
	r := api(t, http.MethodGet, "/v1/me/wallet", u.token, "", "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	return int64(r.body["available_balance_minor"].(float64))
}

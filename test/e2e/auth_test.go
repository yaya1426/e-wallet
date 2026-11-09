//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postJSON(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	method := http.MethodPost
	if body == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

// TestSmoke_RegisterLoginWallet walks the T004 flow against the running stack. The phone is
// random so the test can run again on the same database.
func TestSmoke_RegisterLoginWallet(t *testing.T) {
	waitReady(t)
	phone := fmt.Sprintf("+2010%08d", rand.N(100_000_000))

	status, reg := postJSON(t, walletURL+"/v1/auth/register", "",
		`{"phone":"`+phone+`","full_name":"Smoke Test","password":"smoke-pass-1"}`)
	require.Equal(t, http.StatusCreated, status, reg)

	status, login := postJSON(t, walletURL+"/v1/auth/login", "", `{"phone":"`+phone+`","password":"smoke-pass-1"}`)
	require.Equal(t, http.StatusOK, status, login)

	status, wallet := postJSON(t, walletURL+"/v1/me/wallet", login["access_token"].(string), "")
	require.Equal(t, http.StatusOK, status, wallet)
	assert.Equal(t, reg["wallet_id"], wallet["wallet_id"])
	assert.EqualValues(t, 0, wallet["available_balance_minor"])

	status, again := postJSON(t, walletURL+"/v1/auth/register", "",
		`{"phone":"`+phone+`","full_name":"Smoke Test","password":"smoke-pass-1"}`)
	assert.Equal(t, http.StatusConflict, status, again)
}

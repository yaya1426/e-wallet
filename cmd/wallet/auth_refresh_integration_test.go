//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

func (f apiFixture) login(t *testing.T, phone string) map[string]any {
	t.Helper()
	r := f.call(t, http.MethodPost, "/v1/auth/login", "", fmt.Sprintf(`{"phone":%q,"password":"s3cret-pass"}`, phone))
	require.Equal(t, http.StatusOK, r.status, r.body)
	return r.body
}

func (f apiFixture) refresh(t *testing.T, token string) httpResult {
	t.Helper()
	return f.call(t, http.MethodPost, "/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, token))
}

func newRefreshFixture(t *testing.T) (apiFixture, *clocktest.Fake, party) {
	t.Helper()
	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	f := newAPIFixtureWith(t, clk, nil)
	return f, clk, f.newParty(t, "+201001234567")
}

// AC-1, AC-2.
func TestAuth_RefreshRotation(t *testing.T) {
	f, _, p := newRefreshFixture(t)
	s := f.login(t, p.phone)
	refresh := s["refresh_token"].(string)
	assert.Len(t, refresh, 43)
	assert.EqualValues(t, 2_592_000, s["refresh_expires_in"])
	assert.EqualValues(t, 900, s["expires_in"])
	sum := sha256.Sum256([]byte(refresh))
	var byHash, byText int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM refresh_tokens WHERE token_hash = $1),
		(SELECT count(*) FROM refresh_tokens WHERE token_hash = $2)`, sum[:], []byte(refresh)).Scan(&byHash, &byText))
	assert.Equal(t, 1, byHash, "stored by its hash")
	assert.Zero(t, byText, "never in clear")

	r := f.refresh(t, refresh)
	require.Equal(t, http.StatusOK, r.status, r.body)
	next := r.body["refresh_token"].(string)
	assert.NotEqual(t, refresh, next)
	assert.Equal(t, "Bearer", r.body["token_type"])
	w := f.call(t, http.MethodGet, "/v1/me/wallet", r.body["access_token"].(string), "")
	assert.Equal(t, http.StatusOK, w.status, "the new access token works")

	r = f.refresh(t, next)
	assert.Equal(t, http.StatusOK, r.status, "the rotated token works once")
}

// AC-3: reuse revokes the whole session but not another one.
func TestAuth_RefreshReuseRevokesFamily(t *testing.T) {
	f, _, p := newRefreshFixture(t)
	first := f.login(t, p.phone)["refresh_token"].(string)
	other := f.login(t, p.phone)["refresh_token"].(string) // another device
	second := f.refresh(t, first).body["refresh_token"].(string)

	r := f.refresh(t, first)
	assert.Equal(t, http.StatusUnauthorized, r.status)
	assert.Equal(t, "INVALID_REFRESH_TOKEN", errorCode(r))
	assert.Equal(t, http.StatusUnauthorized, f.refresh(t, second).status, "the thief's or user's newer token is revoked too")
	assert.Equal(t, http.StatusOK, f.refresh(t, other).status, "another session is unaffected")
}

// AC-4.
func TestAuth_RefreshExpiredUnknownDisabled(t *testing.T) {
	f, clk, p := newRefreshFixture(t)
	r := f.refresh(t, "not-a-real-token")
	assert.Equal(t, http.StatusUnauthorized, r.status)
	assert.Equal(t, "INVALID_REFRESH_TOKEN", errorCode(r))
	assert.Equal(t, http.StatusBadRequest, f.call(t, http.MethodPost, "/v1/auth/refresh", "", `{"refresh_token":`).status)

	token := f.login(t, p.phone)["refresh_token"].(string)
	clk.Advance(30*24*time.Hour - time.Second)
	token = f.refresh(t, token).body["refresh_token"].(string) // sliding: valid another 30 days
	clk.Advance(30*24*time.Hour + time.Second)
	assert.Equal(t, http.StatusUnauthorized, f.refresh(t, token).status, "expired")

	clk.Advance(-60 * 24 * time.Hour)
	live := f.login(t, p.phone)["refresh_token"].(string)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE users SET status = 'DISABLED' WHERE phone_e164 = $1`, p.phone)
	require.NoError(t, err)
	r = f.refresh(t, live)
	assert.Equal(t, http.StatusForbidden, r.status)
	assert.Equal(t, "USER_DISABLED", errorCode(r))
}

// AC-5.
func TestAuth_Logout(t *testing.T) {
	f, _, p := newRefreshFixture(t)
	q := f.newParty(t, "+201001234568")
	s := f.login(t, p.phone)
	token, access := s["refresh_token"].(string), s["access_token"].(string)
	otherUser := f.login(t, q.phone)["refresh_token"].(string)

	body := func(tok string) string { return fmt.Sprintf(`{"refresh_token":%q}`, tok) }
	assert.Equal(t, http.StatusUnauthorized, f.call(t, http.MethodPost, "/v1/auth/logout", "", body(token)).status, "needs an access token")
	assert.Equal(t, http.StatusNoContent, f.call(t, http.MethodPost, "/v1/auth/logout", access, body("unknown")).status)
	assert.Equal(t, http.StatusNoContent, f.call(t, http.MethodPost, "/v1/auth/logout", access, body(otherUser)).status)
	assert.Equal(t, http.StatusOK, f.refresh(t, otherUser).status, "another user's session is untouched")

	assert.Equal(t, http.StatusNoContent, f.call(t, http.MethodPost, "/v1/auth/logout", access, body(token)).status)
	assert.Equal(t, http.StatusUnauthorized, f.refresh(t, token).status, "logged out")
}

// AC-6: two refreshes with the same token at once → exactly one succeeds.
func TestAuth_RefreshConcurrent(t *testing.T) {
	f, _, p := newRefreshFixture(t)
	token := f.login(t, p.phone)["refresh_token"].(string)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() { codes[i] = f.refresh(t, token).status })
	}
	wg.Wait()
	assert.ElementsMatch(t, []int{http.StatusOK, http.StatusUnauthorized}, codes)
}

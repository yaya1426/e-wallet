//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f apiFixture) issueResetCode(t *testing.T, phone string) map[string]any {
	t.Helper()
	r := f.internal(t, http.MethodPost, "/internal/v1/users/"+url.PathEscape(phone)+"/password-reset-code", testInternalKey, "")
	require.Equal(t, http.StatusCreated, r.status, r.body)
	return r.body
}

func (f apiFixture) resetPassword(t *testing.T, phone, code, password string) httpResult {
	t.Helper()
	return f.call(t, http.MethodPost, "/v1/auth/password-reset", "",
		fmt.Sprintf(`{"phone":%q,"code":%q,"new_password":%q}`, phone, code, password))
}

func (f apiFixture) loginStatus(t *testing.T, phone, password string) int {
	t.Helper()
	return f.call(t, http.MethodPost, "/v1/auth/login", "", fmt.Sprintf(`{"phone":%q,"password":%q}`, phone, password)).status
}

// T027 AC-1, AC-2.
func TestPasswordReset_Flow(t *testing.T) {
	f, clk := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	session := f.login(t, p.phone)

	first := f.issueResetCode(t, p.phone)
	assert.Equal(t, p.phone, first["phone"])
	assert.Regexp(t, regexp.MustCompile(`^\d{6}$`), first["code"])
	expires, err := time.Parse(time.RFC3339Nano, first["expires_at"].(string))
	require.NoError(t, err)
	assert.Equal(t, clk.Now().Add(15*time.Minute).UTC(), expires.UTC())
	var plain int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM password_reset_codes WHERE code_hash = $1`, []byte(first["code"].(string))).Scan(&plain))
	assert.Zero(t, plain, "only a hash is stored")

	second := f.issueResetCode(t, p.phone)
	if first["code"] != second["code"] {
		r := f.resetPassword(t, p.phone, first["code"].(string), "new-pass-123")
		assert.Equal(t, http.StatusUnauthorized, r.status, "the first code was invalidated")
	}

	r := f.resetPassword(t, p.phone, second["code"].(string), "new-pass-123")
	require.Equal(t, http.StatusNoContent, r.status, r.body)
	assert.Equal(t, http.StatusUnauthorized, f.loginStatus(t, p.phone, "s3cret-pass"), "old password")
	assert.Equal(t, http.StatusOK, f.loginStatus(t, p.phone, "new-pass-123"), "new password")
	assert.Equal(t, http.StatusUnauthorized, f.refresh(t, session["refresh_token"].(string)).status, "signed out everywhere")
	r = f.resetPassword(t, p.phone, second["code"].(string), "another-pass-1")
	assert.Equal(t, http.StatusUnauthorized, r.status, "single use")
	assert.Equal(t, "INVALID_RESET_CODE", errorCode(r))
}

// T027 AC-3, AC-4.
func TestPasswordReset_Failures(t *testing.T) {
	f, clk := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	code := f.issueResetCode(t, p.phone)["code"].(string)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}

	r := f.resetPassword(t, p.phone, code, "short")
	assert.Equal(t, http.StatusBadRequest, r.status)
	assert.Equal(t, "INVALID_PASSWORD", errorCode(r), "not counted as an attempt")

	for i := range 5 {
		r := f.resetPassword(t, p.phone, wrong, "new-pass-123")
		require.Equal(t, http.StatusUnauthorized, r.status, "attempt %d", i+1)
		assert.Equal(t, "INVALID_RESET_CODE", errorCode(r))
	}
	assert.Equal(t, http.StatusUnauthorized, f.resetPassword(t, p.phone, code, "new-pass-123").status, "dead after 5 wrong")

	code = f.issueResetCode(t, p.phone)["code"].(string)
	clk.Advance(16 * time.Minute)
	assert.Equal(t, http.StatusUnauthorized, f.resetPassword(t, p.phone, code, "new-pass-123").status, "expired")

	unknown := f.resetPassword(t, "+201009999999", "123456", "new-pass-123")
	assert.Equal(t, http.StatusUnauthorized, unknown.status)
	assert.Equal(t, "INVALID_RESET_CODE", errorCode(unknown), "same answer for an unknown phone")
	assert.Equal(t, http.StatusOK, f.loginStatus(t, p.phone, "s3cret-pass"), "password unchanged")
}

// T027 AC-5.
func TestPasswordReset_OperatorEndpoint(t *testing.T) {
	f, _ := newOperatorFixture(t)
	path := "/internal/v1/users/" + url.PathEscape("+201001234567") + "/password-reset-code"
	assert.Equal(t, http.StatusUnauthorized, f.internal(t, http.MethodPost, path, "", "").status)
	r := f.internal(t, http.MethodPost, path, testInternalKey, "")
	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Equal(t, "USER_NOT_FOUND", errorCode(r))
	r = f.internal(t, http.MethodPost, "/internal/v1/users/12345/password-reset-code", testInternalKey, "")
	assert.Equal(t, http.StatusBadRequest, r.status)
	assert.Equal(t, "INVALID_PHONE", errorCode(r))

	// The reset endpoint shares the login limit (10 per phone per minute).
	p := f.newParty(t, "+201001234567")
	for range 10 {
		f.resetPassword(t, p.phone, "000000", "new-pass-123")
	}
	assert.Equal(t, http.StatusTooManyRequests, f.resetPassword(t, p.phone, "000000", "new-pass-123").status)
}

// errorWithoutRequestID is the error body without its per-request request_id, so bodies of
// different requests can be compared.
func errorWithoutRequestID(t *testing.T, r httpResult) map[string]any {
	t.Helper()
	e, ok := r.body["error"].(map[string]any)
	require.True(t, ok, r.body)
	out := map[string]any{}
	for k, v := range e {
		if k != "request_id" {
			out[k] = v
		}
	}
	return out
}

// T029 AC-1, AC-2 (TDR-021 §1): a reset ends access tokens issued before it.
func TestPasswordReset_RevokesAccessTokens(t *testing.T) {
	f, clk := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	other := f.newParty(t, "+201001234568")
	require.Equal(t, http.StatusOK, f.call(t, http.MethodGet, "/v1/me/wallet", p.token, "").status)

	clk.Advance(time.Second) // the old token's iat is a whole second before the reset
	code := f.issueResetCode(t, p.phone)["code"].(string)
	require.Equal(t, http.StatusNoContent, f.resetPassword(t, p.phone, code, "new-pass-123").status)

	r := f.call(t, http.MethodGet, "/v1/me/wallet", p.token, "")
	assert.Equal(t, http.StatusUnauthorized, r.status, "old access token")
	assert.Equal(t, "UNAUTHENTICATED", errorCode(r))

	fresh := f.call(t, http.MethodPost, "/v1/auth/login", "", fmt.Sprintf(`{"phone":%q,"password":"new-pass-123"}`, p.phone))
	require.Equal(t, http.StatusOK, fresh.status, fresh.body)
	assert.Equal(t, http.StatusOK, f.call(t, http.MethodGet, "/v1/me/wallet", fresh.body["access_token"].(string), "").status,
		"a token from a login after the reset (same second) works")

	clk.Advance(time.Minute)
	assert.Equal(t, http.StatusOK, f.call(t, http.MethodGet, "/v1/me/wallet", other.token, "").status,
		"another user's token (password never changed) is unaffected")
	var changed *time.Time
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT password_changed_at FROM users WHERE phone_e164 = $1`, other.phone).Scan(&changed))
	assert.Nil(t, changed, "never changed = NULL")
}

// T029 AC-3 (TDR-021 §2).
func TestPasswordReset_DisabledUser(t *testing.T) {
	f, _ := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	code := f.issueResetCode(t, p.phone)["code"].(string)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE users SET status = 'DISABLED' WHERE phone_e164 = $1`, p.phone)
	require.NoError(t, err)

	r := f.internal(t, http.MethodPost, "/internal/v1/users/"+url.PathEscape(p.phone)+"/password-reset-code", testInternalKey, "")
	assert.Equal(t, http.StatusForbidden, r.status)
	assert.Equal(t, "USER_DISABLED", errorCode(r))
	var codes int
	require.NoError(t, f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM password_reset_codes`).Scan(&codes))
	assert.Equal(t, 1, codes, "no code row written for a disabled user")

	r = f.resetPassword(t, p.phone, code, "new-pass-123")
	assert.Equal(t, http.StatusUnauthorized, r.status)
	assert.Equal(t, "INVALID_RESET_CODE", errorCode(r))
	var attempts int
	var usedAt *time.Time
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT attempts, used_at FROM password_reset_codes`).Scan(&attempts, &usedAt))
	assert.Zero(t, attempts, "not counted")
	assert.Nil(t, usedAt, "not used")

	_, err = f.db.Pool().Exec(context.Background(), `UPDATE users SET status = 'ACTIVE' WHERE phone_e164 = $1`, p.phone)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, f.loginStatus(t, p.phone, "s3cret-pass"), "password unchanged")
}

// T029 AC-4: one code, two concurrent resets → exactly one wins.
func TestPasswordReset_ConcurrentUse(t *testing.T) {
	f, _ := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	code := f.issueResetCode(t, p.phone)["code"].(string)

	passwords := []string{"winner-pass-1", "winner-pass-2"}
	statuses := make([]int, len(passwords))
	var wg sync.WaitGroup
	for i, pw := range passwords {
		wg.Go(func() { statuses[i] = f.resetPassword(t, p.phone, code, pw).status })
	}
	wg.Wait()

	assert.ElementsMatch(t, []int{http.StatusNoContent, http.StatusUnauthorized}, statuses)
	winner := passwords[0]
	if statuses[1] == http.StatusNoContent {
		winner = passwords[1]
	}
	assert.Equal(t, http.StatusOK, f.loginStatus(t, p.phone, winner), "the winner's password is set")
}

// T029 AC-5: concurrent wrong guesses cannot exceed the 5-attempt cap.
func TestPasswordReset_ConcurrentWrongGuesses(t *testing.T) {
	f, _ := newOperatorFixture(t)
	p := f.newParty(t, "+201001234567")
	code := f.issueResetCode(t, p.phone)["code"].(string)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}

	const guesses = 8 // + 1 right try below stays within the 10-per-phone limit
	var wg sync.WaitGroup
	for range guesses {
		wg.Go(func() {
			r := f.resetPassword(t, p.phone, wrong, "new-pass-123")
			assert.Equal(t, http.StatusUnauthorized, r.status)
		})
	}
	wg.Wait()

	var attempts int
	var invalidated *time.Time
	require.NoError(t, f.db.Pool().QueryRow(context.Background(),
		`SELECT attempts, invalidated_at FROM password_reset_codes`).Scan(&attempts, &invalidated))
	assert.Equal(t, 5, attempts)
	assert.NotNil(t, invalidated, "the code is dead")
	assert.Equal(t, http.StatusUnauthorized, f.resetPassword(t, p.phone, code, "new-pass-123").status, "even the right code")
}

// T029 AC-6: every phone/code failure looks the same.
func TestPasswordReset_UniformFailures(t *testing.T) {
	f, clk := newOperatorFixture(t)
	wrongCode := f.newParty(t, "+201001234561")
	expired := f.newParty(t, "+201001234562")
	disabled := f.newParty(t, "+201001234563")

	right := f.issueResetCode(t, wrongCode.phone)["code"].(string)
	wrong := "000000"
	if right == wrong {
		wrong = "111111"
	}
	expiredCode := f.issueResetCode(t, expired.phone)["code"].(string)
	disabledCode := f.issueResetCode(t, disabled.phone)["code"].(string)
	_, err := f.db.Pool().Exec(context.Background(), `UPDATE users SET status = 'DISABLED' WHERE phone_e164 = $1`, disabled.phone)
	require.NoError(t, err)

	results := map[string]httpResult{
		"unknown phone": f.resetPassword(t, "+201009999999", "123456", "new-pass-123"),
		"wrong code":    f.resetPassword(t, wrongCode.phone, wrong, "new-pass-123"),
		"disabled user": f.resetPassword(t, disabled.phone, disabledCode, "new-pass-123"),
	}
	clk.Advance(16 * time.Minute)
	results["expired code"] = f.resetPassword(t, expired.phone, expiredCode, "new-pass-123")

	want := errorWithoutRequestID(t, results["unknown phone"])
	for name, r := range results {
		assert.Equal(t, http.StatusUnauthorized, r.status, name)
		assert.Equal(t, want, errorWithoutRequestID(t, r), name)
	}
}

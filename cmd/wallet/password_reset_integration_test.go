//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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

//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalKey is the operator key; the dev key of compose locally, E2E_INTERNAL_API_KEY when
// remote (no default there).
var internalKey = func() string {
	if k := os.Getenv("E2E_INTERNAL_API_KEY"); k != "" || remote {
		return k
	}
	return "dev-only-internal-api-key-0123456789abcdef"
}()

func loginSession(t *testing.T, phone, password string) apiResponse {
	t.Helper()
	return loginPolitely(t, phone, password)
}

// TDR-018 §3: a session refreshes with rotation and ends with logout.
func TestE2E_RefreshAndLogout(t *testing.T) {
	scenario(t)
	u := newUser(t, "Session User")
	s := loginSession(t, u.phone, "e2e-pass-123")
	require.Equal(t, http.StatusOK, s.status, s.body)
	first := s.body["refresh_token"].(string)

	r := api(t, http.MethodPost, "/v1/auth/refresh", "", "", fmt.Sprintf(`{"refresh_token":%q}`, first))
	require.Equal(t, http.StatusOK, r.status, r.body)
	second := r.body["refresh_token"].(string)
	access := r.body["access_token"].(string)
	assert.Equal(t, http.StatusOK, api(t, http.MethodGet, "/v1/me/wallet", access, "", "").status)

	reuse := api(t, http.MethodPost, "/v1/auth/refresh", "", "", fmt.Sprintf(`{"refresh_token":%q}`, first))
	assert.Equal(t, http.StatusUnauthorized, reuse.status)
	assert.Equal(t, "INVALID_REFRESH_TOKEN", reuse.errorCode())
	assert.Equal(t, http.StatusUnauthorized,
		api(t, http.MethodPost, "/v1/auth/refresh", "", "", fmt.Sprintf(`{"refresh_token":%q}`, second)).status,
		"reuse revoked the session")

	s = loginSession(t, u.phone, "e2e-pass-123")
	token := s.body["refresh_token"].(string)
	out := api(t, http.MethodPost, "/v1/auth/logout", s.body["access_token"].(string), "", fmt.Sprintf(`{"refresh_token":%q}`, token))
	assert.Equal(t, http.StatusNoContent, out.status)
	assert.Equal(t, http.StatusUnauthorized,
		api(t, http.MethodPost, "/v1/auth/refresh", "", "", fmt.Sprintf(`{"refresh_token":%q}`, token)).status)
}

// operator calls an /internal/v1 endpoint with the operator key.
func operator(t *testing.T, method, path string) apiResponse {
	t.Helper()
	return apiWithHeader(t, method, path, "X-Internal-Api-Key", internalKey)
}

// TDR-018 §4: an operator issues a code; the user sets a new password with it.
func TestE2E_PasswordReset(t *testing.T) {
	if internalKey == "" {
		t.Skip("needs the operator key: set E2E_INTERNAL_API_KEY")
	}
	scenario(t)
	u := newUser(t, "Reset User")
	code := operator(t, http.MethodPost, "/internal/v1/users/"+url.PathEscape(u.phone)+"/password-reset-code")
	require.Equal(t, http.StatusCreated, code.status, code.body)

	r := api(t, http.MethodPost, "/v1/auth/password-reset", "", "",
		fmt.Sprintf(`{"phone":%q,"code":%q,"new_password":"reset-pass-456"}`, u.phone, code.body["code"]))
	require.Equal(t, http.StatusNoContent, r.status, r.body)
	assert.Equal(t, http.StatusUnauthorized, loginSession(t, u.phone, "e2e-pass-123").status, "old password")
	assert.Equal(t, http.StatusOK, loginSession(t, u.phone, "reset-pass-456").status, "new password")

	again := api(t, http.MethodPost, "/v1/auth/password-reset", "", "",
		fmt.Sprintf(`{"phone":%q,"code":%q,"new_password":"other-pass-789"}`, u.phone, code.body["code"]))
	assert.Equal(t, http.StatusUnauthorized, again.status)
	assert.Equal(t, "INVALID_RESET_CODE", again.errorCode())

	health := operator(t, http.MethodGet, "/internal/v1/health/ops")
	if health.status != http.StatusNotFound { // older deployments without T028
		require.Equal(t, http.StatusOK, health.status, health.body)
		assert.Contains(t, []any{"ok", "warning", "critical"}, health.body["status"])
	}
}

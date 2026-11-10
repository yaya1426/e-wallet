//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

// postFrom posts body to path as a client whose IP the proxy reports in do-connecting-ip.
func (f apiFixture) postFrom(t *testing.T, ip, path, body string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("do-connecting-ip", ip)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func registration(n int) string {
	return fmt.Sprintf(`{"phone":"+2010%08d","full_name":"Rate Limit","password":"s3cret-pass"}`, n)
}

// T024 AC-3: 5 registrations per client IP per hour.
func TestRegister_RateLimited(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	f := newAPIFixtureWith(t, clk, map[string]string{"CLIENT_IP_HEADER": "do-connecting-ip", "REGISTER_LIMIT_PER_HOUR": "5"})

	for i := range 5 {
		assert.Equal(t, http.StatusCreated, f.postFrom(t, "198.51.100.1", "/v1/auth/register", registration(i)))
	}
	assert.Equal(t, http.StatusTooManyRequests, f.postFrom(t, "198.51.100.1", "/v1/auth/register", registration(5)))
	assert.Equal(t, http.StatusCreated, f.postFrom(t, "198.51.100.2", "/v1/auth/register", registration(6)), "another client")
	clk.Advance(time.Hour)
	assert.Equal(t, http.StatusCreated, f.postFrom(t, "198.51.100.1", "/v1/auth/register", registration(7)), "a new hour")
}

// T024 AC-2: behind the proxy each client has its own login budget.
func TestLogin_PerClientIPBehindProxy(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	f := newAPIFixtureWith(t, clk, map[string]string{"CLIENT_IP_HEADER": "do-connecting-ip"})
	login := func(n int) string { return fmt.Sprintf(`{"phone":"+2011%08d","password":"wrong-pass"}`, n) }

	for i := range 30 {
		require.Equal(t, http.StatusUnauthorized, f.postFrom(t, "198.51.100.1", "/v1/auth/login", login(i)), "attempt %d", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, f.postFrom(t, "198.51.100.1", "/v1/auth/login", login(30)))
	assert.Equal(t, http.StatusUnauthorized, f.postFrom(t, "198.51.100.2", "/v1/auth/login", login(31)),
		"another client is not blocked")
}

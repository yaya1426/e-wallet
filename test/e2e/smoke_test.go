//go:build e2e

// Package e2e holds black-box tests against the running docker compose stack
// (`make up`, then `make e2e`). WALLET_URL and BANKSIM_URL override the default addresses.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readyTimeout is how long the smoke test waits for the stack to become ready.
const readyTimeout = 60 * time.Second

func baseURL(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return strings.TrimRight(v, "/")
	}
	return fallback
}

var (
	walletURL  = baseURL("WALLET_URL", "http://localhost:8080")
	banksimURL = baseURL("BANKSIM_URL", "http://localhost:9090")
	// remote: the suite runs against a deployed wallet (WALLET_URL set), e.g. DigitalOcean
	// (T028). There the simulator is internal and the database is not reachable unless
	// BANKSIM_URL / E2E_DATABASE_URL are given; the checks that need them are skipped.
	remote = os.Getenv("WALLET_URL") != ""
	// banksimReachable: the simulator can be called directly.
	banksimReachable = !remote || os.Getenv("BANKSIM_URL") != ""
)

type response struct {
	status    int
	requestID string
	body      string
}

func get(t *testing.T, url string) response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return response{status: resp.StatusCode, requestID: resp.Header.Get("X-Request-ID"), body: string(body)}
}

// waitReady polls /readyz until it answers 200 or the timeout passes.
func waitReady(t *testing.T) response {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, walletURL+"/readyz", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		if err == nil && resp.StatusCode == http.StatusOK {
			return get(t, walletURL+"/readyz")
		}
		if time.Now().After(deadline) {
			t.Fatalf("wallet not ready after %s", readyTimeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestSmoke_Healthz(t *testing.T) {
	waitReady(t)
	urls := []string{walletURL + "/healthz"}
	if banksimReachable {
		urls = append(urls, banksimURL+"/healthz")
	}
	for _, url := range urls {
		r := get(t, url)
		assert.Equal(t, http.StatusOK, r.status, url)
		assert.JSONEq(t, `{"status":"ok"}`, r.body, url)
		assert.NotEmpty(t, r.requestID, url)
	}
}

func TestSmoke_Readyz(t *testing.T) {
	r := waitReady(t)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &body))
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, map[string]string{"database": "ok", "migrations": "ok"}, body.Checks)
}

func TestSmoke_Metrics(t *testing.T) {
	waitReady(t)
	get(t, walletURL+"/healthz")

	r := get(t, walletURL+"/metrics")

	assert.Equal(t, http.StatusOK, r.status)
	assert.Contains(t, r.body, "go_goroutines")
	assert.Contains(t, r.body, "http_requests_total")
}

func TestSmoke_NotFound(t *testing.T) {
	waitReady(t)

	r := get(t, walletURL+"/does-not-exist")

	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Contains(t, r.body, `"code":"NOT_FOUND"`)
}

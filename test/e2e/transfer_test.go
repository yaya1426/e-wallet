//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func transferBody(phone string, minor int64) string {
	return fmt.Sprintf(`{"recipient_phone":%q,"amount_minor":%d,"currency":"EGP","note":"e2e"}`, phone, minor)
}

func historyItems(t *testing.T, u user) []map[string]any {
	t.Helper()
	r := api(t, http.MethodGet, "/v1/me/transactions", u.token, "", "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	raw := r.body["items"].([]any)
	items := make([]map[string]any, len(raw))
	for i, it := range raw {
		items[i] = it.(map[string]any)
	}
	return items
}

// Scenario 4: A tops up and sends to B; both balances and both histories are right (PRD-004).
func TestE2E_Transfer(t *testing.T) {
	scenario(t)
	a, b := newUser(t, "Ahmed Mohamed Ali"), newUser(t, "Sara Hassan")
	a.fund(t, 50_000)

	preview := api(t, http.MethodGet, "/v1/recipients?phone="+url.QueryEscape(b.phone), a.token, "", "")
	require.Equal(t, http.StatusOK, preview.status, preview.body)
	assert.Equal(t, b.phone, preview.body["phone"])
	assert.Equal(t, "Sara H.", preview.body["masked_name"])

	r := api(t, http.MethodPost, "/v1/transfers", a.token, uuid.NewString(), transferBody(b.phone, 12_500))
	require.Equal(t, http.StatusCreated, r.status, r.body)
	assert.Equal(t, "COMPLETED", r.body["status"])
	assert.EqualValues(t, 37_500, r.body["balance_after_minor"])

	assert.Equal(t, int64(37_500), a.balance(t))
	assert.Equal(t, int64(12_500), b.balance(t))

	out := historyItems(t, a)[0]
	assert.Equal(t, r.body["transaction_id"], out["id"])
	assert.Equal(t, "P2P", out["type"])
	assert.Equal(t, "OUT", out["direction"])
	assert.Equal(t, b.phone, out["counterparty"].(map[string]any)["phone"])
	in := historyItems(t, b)
	require.Len(t, in, 1)
	assert.Equal(t, "IN", in[0]["direction"])
	assert.Equal(t, a.phone, in[0]["counterparty"].(map[string]any)["phone"])
	assert.Equal(t, "e2e", in[0]["note"])
}

// Scenario 5: 20 parallel transfers of 1 000 from a balance of 10 000 → exactly 10 succeed,
// the balance ends at 0 and never goes negative (PRD-004, TDR-010).
func TestE2E_ParallelOverspend(t *testing.T) {
	scenario(t)
	a, b := newUser(t, "Parallel Sender"), newUser(t, "Parallel Receiver")
	a.fund(t, 10_000)

	results := make([]apiResponse, 20)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = api(t, http.MethodPost, "/v1/transfers", a.token, uuid.NewString(), transferBody(b.phone, 1_000))
		})
	}
	wg.Wait()

	sent, refused := 0, 0
	for _, r := range results {
		switch r.status {
		case http.StatusCreated:
			sent++
		case http.StatusUnprocessableEntity:
			assert.Equal(t, "INSUFFICIENT_FUNDS", r.errorCode())
			refused++
		default:
			t.Errorf("unexpected answer %d %v", r.status, r.body)
		}
	}
	assert.Equal(t, 10, sent)
	assert.Equal(t, 10, refused)
	assert.Zero(t, a.balance(t))
	assert.Equal(t, int64(10_000), b.balance(t))
}

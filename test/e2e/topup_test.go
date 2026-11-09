//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenario 1: a top-up the bank confirms credits the wallet (PRD-002 AC-1, AC-2).
func TestE2E_TopUpSuccess(t *testing.T) {
	scenario(t)
	u := newUser(t, "Top Up")
	u.linkBank(t, ibanSuccess)

	r := u.request(t, "/v1/topups", 50_000, "")
	assert.Zero(t, u.balance(t), "no money before the bank confirms")
	final := u.waitFinal(t, r.body["transaction_id"].(string))

	assert.Equal(t, "COMPLETED", final["status"])
	assert.Equal(t, "TOPUP", final["type"])
	assert.Equal(t, "IN", final["direction"])
	assert.NotNil(t, final["completed_at"])
	assert.Equal(t, int64(50_000), u.balance(t))
}

// Scenario 2: the bank refuses the debit, later (…0001) or at once (…0002); no money moves
// (PRD-002 AC-3).
func TestE2E_TopUpRejected(t *testing.T) {
	for _, tt := range []struct{ iban, code string }{
		{ibanFailLate, "INSUFFICIENT_FUNDS"},
		{ibanReject, "INVALID_ACCOUNT"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			scenario(t)
			u := newUser(t, "Top Up Rejected")
			u.linkBank(t, tt.iban)

			r := u.request(t, "/v1/topups", 50_000, "")
			final := u.waitFinal(t, r.body["transaction_id"].(string))

			assert.Equal(t, "FAILED", final["status"])
			assert.Equal(t, tt.code, final["failure_code"])
			assert.NotEmpty(t, final["failure_message"])
			assert.Zero(t, u.balance(t))
		})
	}
}

// Scenario 3: a double click (the same key twice) creates one top-up; the second answer is
// the replay (PRD-002 AC-5).
func TestE2E_DoubleClick(t *testing.T) {
	scenario(t)
	u := newUser(t, "Double Click")
	u.linkBank(t, ibanSuccess)
	key := uuid.NewString()

	first := u.request(t, "/v1/topups", 20_000, key)
	second := api(t, http.MethodPost, "/v1/topups", u.token, key, amountBody(20_000))

	require.Equal(t, http.StatusAccepted, second.status, second.body)
	assert.False(t, first.replayed)
	assert.True(t, second.replayed)
	assert.Equal(t, first.body["transaction_id"], second.body["transaction_id"])
	u.waitFinal(t, first.body["transaction_id"].(string))
	assert.Equal(t, int64(20_000), u.balance(t), "credited once")

	history := api(t, http.MethodGet, "/v1/me/transactions", u.token, "", "")
	require.Equal(t, http.StatusOK, history.status)
	assert.Len(t, history.body["items"], 1, "one transaction")
}

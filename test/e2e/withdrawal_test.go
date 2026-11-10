//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Scenario 6: withdrawals are reserved at once; the bank pays out (…0000), fails it later
// (…0001) or refuses it at once (…0002); a failure gives the money back (PRD-003 AC-1, 3, 4).
func TestE2E_Withdrawal(t *testing.T) {
	for _, tt := range []struct {
		name, iban, status, code string
		balanceAfter             int64
	}{
		{"paid", ibanSuccess, "COMPLETED", "", 70_000},
		{"failed later", ibanFailLate, "FAILED", "ACCOUNT_CLOSED", 100_000},
		{"refused at once", ibanReject, "FAILED", "INVALID_ACCOUNT", 100_000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scenario(t)
			u := newUser(t, "Withdrawal")
			u.fund(t, 100_000)
			u.linkBank(t, tt.iban)

			r := u.request(t, "/v1/withdrawals", 30_000, "")
			if tt.iban != ibanReject { // a sync refusal may already be refunded by now
				assert.Equal(t, int64(70_000), u.balance(t), "reserved at once")
			}
			final := u.waitFinal(t, r.body["transaction_id"].(string))

			assert.Equal(t, tt.status, final["status"])
			assert.Equal(t, "WITHDRAWAL", final["type"])
			assert.Equal(t, "OUT", final["direction"])
			if tt.code != "" {
				assert.Equal(t, tt.code, final["failure_code"])
			}
			assert.Equal(t, tt.balanceAfter, u.balance(t))
		})
	}
}

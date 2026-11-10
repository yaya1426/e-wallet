//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webhookSecret is the dev-only secret compose shares between the wallet and banksim.
var webhookSecret = baseURL("E2E_BANK_WEBHOOK_SECRET", "dev-only-bank-webhook-secret-0123456789")

// postSignedWebhook signs body with secret like the bank (TDR-007 §1) and posts it to the wallet.
func postSignedWebhook(t *testing.T, secret string, body []byte) int {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, walletURL+"/webhooks/bank", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bank-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Scenario 7: a copy of an already-applied debit.succeeded (new event_id, same reference and
// amount) is accepted with 200 and changes nothing (PRD-002 AC-4, TDR-005 L4).
func TestE2E_DuplicateWebhook(t *testing.T) {
	requireDB(t)
	scenario(t)
	u := newUser(t, "Duplicate Webhook")
	u.linkBank(t, ibanSuccess)
	r := u.request(t, "/v1/topups", 40_000, "")
	txnID := r.body["transaction_id"].(string)
	require.Equal(t, "COMPLETED", u.waitFinal(t, txnID)["status"])

	var ref string
	require.NoError(t, pool(t).QueryRow(context.Background(),
		`SELECT bank_reference FROM transactions WHERE id = $1`, txnID).Scan(&ref))
	eventID := uuid.NewString()
	body, err := json.Marshal(map[string]any{
		"event_id": eventID, "event_type": "debit.succeeded", "bank_reference": ref, "idempotency_key": txnID,
		"amount_minor": 40_000, "currency": "EGP", "status": "SUCCEEDED", "occurred_at": time.Now().UTC(),
	})
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, postSignedWebhook(t, webhookSecret, body))
	assert.Equal(t, http.StatusOK, postSignedWebhook(t, webhookSecret, body), "the same event again is stored once")

	require.Eventually(t, func() bool {
		var status string
		err := pool(t).QueryRow(context.Background(),
			`SELECT status FROM inbox_events WHERE provider_event_id = $1`, eventID).Scan(&status)
		return err == nil && status == "DONE"
	}, finalTimeout, 100*time.Millisecond, "the copy is processed")
	assert.Equal(t, int64(40_000), u.balance(t), "credited once")
	var entries, discrepancies, rows int
	require.NoError(t, pool(t).QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM journal_entries WHERE transaction_id = $1),
		(SELECT count(*) FROM reconciliation_discrepancies WHERE transaction_id = $1),
		(SELECT count(*) FROM inbox_events WHERE provider_event_id = $2)`, txnID, eventID).Scan(&entries, &discrepancies, &rows))
	assert.Equal(t, 1, entries)
	assert.Zero(t, discrepancies)
	assert.Equal(t, 1, rows)

	// A copy signed with another secret is refused.
	assert.Equal(t, http.StatusUnauthorized, postSignedWebhook(t, "not-the-secret-not-the-secret-not-the-secret", body))
}

package webhook

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPayload() map[string]any {
	return map[string]any{
		"event_id": "0195f0c8-0000-7000-8000-000000000001", "event_type": EventPayoutFailed,
		"bank_reference": "BSIM-1", "idempotency_key": "0195f0c8-0000-7000-8000-0000000000aa",
		"amount_minor": 15000, "currency": "EGP", "status": StatusFailed,
		"failure_code": "ACCOUNT_CLOSED", "failure_message": "closed", "occurred_at": "2026-11-09T10:00:00Z",
		"future_field": true, // unknown fields are ignored
	}
}

func encode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestDecodeEvent_Valid(t *testing.T) {
	ev, err := decodeEvent(encode(t, validPayload()))
	require.NoError(t, err)
	assert.Equal(t, "0195f0c8-0000-7000-8000-000000000001", ev.EventID)
	assert.Equal(t, EventPayoutFailed, ev.Type)
	assert.Equal(t, "BSIM-1", ev.BankReference)
	assert.Equal(t, uuid.MustParse("0195f0c8-0000-7000-8000-0000000000aa"), ev.IdempotencyKey)
	assert.Equal(t, int64(15000), ev.Amount.Amount())
	assert.Equal(t, "EGP", string(ev.Amount.Currency()))
	assert.False(t, ev.Succeeded())
	assert.Equal(t, "ACCOUNT_CLOSED", ev.FailureCode)
	assert.Equal(t, "closed", ev.FailureMessage)
	assert.True(t, ev.OccurredAt.Equal(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)))
}

func TestDecodeEvent_EveryTypeWithItsStatus(t *testing.T) {
	for typ, status := range statusOf {
		p := validPayload()
		p["event_type"], p["status"] = typ, status
		ev, err := decodeEvent(encode(t, p))
		require.NoError(t, err, typ)
		assert.Equal(t, status == StatusSucceeded, ev.Succeeded(), typ)
	}
}

func TestDecodeEvent_Rejects(t *testing.T) {
	tests := []struct {
		name   string
		change func(m map[string]any)
		want   string
	}{
		{"unknown type", func(m map[string]any) { m["event_type"] = "refund.succeeded" }, "unknown event_type"},
		{"missing event_id", func(m map[string]any) { delete(m, "event_id") }, "event_id"},
		{"missing bank_reference", func(m map[string]any) { delete(m, "bank_reference") }, "bank_reference"},
		{"missing idempotency_key", func(m map[string]any) { delete(m, "idempotency_key") }, "idempotency_key"},
		{"bad idempotency_key", func(m map[string]any) { m["idempotency_key"] = "nope" }, "undecodable"},
		{"zero amount", func(m map[string]any) { m["amount_minor"] = 0 }, "amount_minor"},
		{"negative amount", func(m map[string]any) { m["amount_minor"] = -1 }, "amount_minor"},
		{"fractional amount", func(m map[string]any) { m["amount_minor"] = 1.5 }, "undecodable"},
		{"other currency", func(m map[string]any) { m["currency"] = "USD" }, "currency"},
		{"status does not match type", func(m map[string]any) { m["status"] = StatusSucceeded }, "does not match"},
		{"missing occurred_at", func(m map[string]any) { delete(m, "occurred_at") }, "occurred_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPayload()
			tt.change(p)
			_, err := decodeEvent(encode(t, p))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
	_, err := decodeEvent([]byte(`[1,2]`))
	assert.ErrorContains(t, err, "undecodable")
}

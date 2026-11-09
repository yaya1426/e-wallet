package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/banksim"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

var (
	secret = []byte("test-webhook-secret-of-32-bytes!")
	body   = []byte(`{"event_id":"x"}`)
	now    = time.Unix(1762682400, 0)
)

func sign(key []byte, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func header(ts int64, sigs ...string) string {
	h := "t=" + strconv.FormatInt(ts, 10)
	for _, s := range sigs {
		h += ",v1=" + s
	}
	return h
}

// AC-7: the vector from T014 (banksim's TestSign_KnownVector).
func TestVerify_KnownVector(t *testing.T) {
	h := "t=1762682400,v1=b6625751ed23392018d345cd665e5ef0b4664c0660f71789ed110eaafe0e3bb8"
	require.NoError(t, webhook.Verify(secret, h, body, now))
}

// AC-7: what the simulator signs, the wallet verifies.
func TestVerify_SignedByBanksim(t *testing.T) {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	require.NoError(t, webhook.Verify(secret, banksim.Sign(secret, at, body), body, at.Add(time.Minute)))
}

// AC-3.
func TestVerify(t *testing.T) {
	ts := now.Unix()
	good := sign(secret, ts, body)
	tests := []struct {
		name   string
		header string
		body   []byte
		now    time.Time
		ok     bool
	}{
		{"valid", header(ts, good), body, now, true},
		{"valid, spaces after commas", "t=" + strconv.FormatInt(ts, 10) + ", v1=" + good, body, now, true},
		{"one of several v1 matches", header(ts, sign([]byte("old-secret-of-at-least-32-bytes!!"), ts, body), good), body, now, true},
		{"unknown scheme ignored", header(ts, good) + ",v0=abc", body, now, true},
		{"t 4m59s old", header(ts, good), body, now.Add(4*time.Minute + 59*time.Second), true},
		{"t exactly 5m old", header(ts, good), body, now.Add(5 * time.Minute), true},
		{"wrong secret", header(ts, sign([]byte("another-secret-of-32-bytes-long!"), ts, body)), body, now, false},
		{"tampered body", header(ts, good), []byte(`{"event_id":"y"}`), now, false},
		{"missing header", "", body, now, false},
		{"no v1", header(ts), body, now, false},
		{"no t", "v1=" + good, body, now, false},
		{"t not a number", "t=abc,v1=" + good, body, now, false},
		{"two t values", header(ts, good) + ",t=" + strconv.FormatInt(ts, 10), body, now, false},
		{"v1 not hex", header(ts, "zz"), body, now, false},
		{"v1 wrong length", header(ts, good[:10]), body, now, false},
		{"part without =", header(ts, good) + ",garbage", body, now, false},
		{"t 6 minutes old", header(ts, good), body, now.Add(6 * time.Minute), false},
		{"t 6 minutes in the future", header(ts, good), body, now.Add(-6 * time.Minute), false},
		{"signed with another t", header(ts+1, good), body, now, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := webhook.Verify(secret, tt.header, tt.body, tt.now)
			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, webhook.ErrInvalidSignature)
			}
		})
	}
}

func TestVerify_ErrorHidesSecrets(t *testing.T) {
	ts := now.Unix()
	sig := sign([]byte("another-secret-of-32-bytes-long!"), ts, body)
	err := webhook.Verify(secret, header(ts, sig), body, now)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), sig)
	assert.NotContains(t, err.Error(), string(secret))
}

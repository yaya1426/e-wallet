package banksim

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// SignatureHeader carries the webhook signature (TDR-007 §1).
const SignatureHeader = "X-Bank-Signature"

// Sign returns the X-Bank-Signature value for body sent at t:
// "t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<unix seconds>." + body)>".
func Sign(secret []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

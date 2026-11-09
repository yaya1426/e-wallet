package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader carries the bank's signature: t=<unix seconds>,v1=<hex HMAC-SHA256> (TDR-007 §1).
const SignatureHeader = "X-Bank-Signature"

// SignatureTolerance is the largest accepted gap between the signature time and now, in
// either direction (replay protection, TDR-007 §1).
const SignatureTolerance = 5 * time.Minute

// ErrInvalidSignature means the signature header is missing or malformed, no v1 value
// matches, or its time is outside SignatureTolerance.
var ErrInvalidSignature = errors.New("webhook: invalid signature")

// Verify checks header against body: v1 = hex(HMAC-SHA256(secret, t + "." + body)). The
// header may carry several v1 values (e.g. during a secret rotation); any match is
// accepted. The comparison is constant-time. The error never contains the secret or the
// signature.
func Verify(secret []byte, header string, body []byte, now time.Time) error {
	var (
		ts         string
		signatures [][]byte
	)
	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrInvalidSignature
		}
		switch key {
		case "t":
			if ts != "" {
				return ErrInvalidSignature // two timestamps: ambiguous
			}
			ts = value
		case "v1":
			sig, err := hex.DecodeString(value)
			if err != nil || len(sig) != sha256.Size {
				return ErrInvalidSignature
			}
			signatures = append(signatures, sig)
		}
		// Other keys (future schemes) are ignored.
	}
	if ts == "" || len(signatures) == 0 {
		return ErrInvalidSignature
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	if gap := now.Sub(time.Unix(unix, 0)).Abs(); gap > SignatureTolerance {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := mac.Sum(nil)
	matched := false
	for _, sig := range signatures {
		// Check every value so the time taken does not depend on which one matched.
		if hmac.Equal(sig, want) {
			matched = true
		}
	}
	if !matched {
		return ErrInvalidSignature
	}
	return nil
}

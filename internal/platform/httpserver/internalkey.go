package httpserver

import (
	"crypto/subtle"
	"net/http"
)

// HeaderInternalAPIKey carries the operator key on /internal/v1/* (TDR-018 §5).
const HeaderInternalAPIKey = "X-Internal-Api-Key" //nolint:gosec // G101 false positive: a header name, not a credential

// CodeUnauthenticated answers a request without valid credentials.
const CodeUnauthenticated = "UNAUTHENTICATED"

// RequireInternalKey lets a request through only when its X-Internal-Api-Key equals key
// (constant-time comparison); otherwise 401 UNAUTHENTICATED. key must not be empty.
func RequireInternalKey(key string) func(http.Handler) http.Handler {
	want := []byte(key)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := []byte(r.Header.Get(HeaderInternalAPIKey))
			if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
				WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "Missing or invalid internal API key.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

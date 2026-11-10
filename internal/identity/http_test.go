package identity

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

// newUnitHandler builds a handler without a database: every RequireAuth case below fails
// on the token before the user lookup.
func newUnitHandler(t *testing.T, clk *clocktest.Fake) (*Handler, tokens) {
	t.Helper()
	svc, err := NewService(nil, nil, clk, Config{JWTSecret: testSecret, JWTTTL: 15 * time.Minute, BcryptCost: bcrypt.MinCost})
	require.NoError(t, err)
	return NewHandler(svc, NewLoginLimiter(clk), NewRegisterLimiter(5, clk), slog.New(slog.DiscardHandler)), svc.tokens
}

func TestRequireAuth(t *testing.T) {
	clk := newFakeClock()
	h, tk := newUnitHandler(t, clk)
	issuedNow, err := tk.issue(uuid.New())
	require.NoError(t, err)
	other := tokens{secret: []byte(strings.Repeat("o", 40)), ttl: time.Minute, clock: clk}
	otherSigned, err := other.issue(uuid.New())
	require.NoError(t, err)
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: uuid.New().String(), ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(clk.Now()),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	clk.Advance(16 * time.Minute) // issuedNow is now expired

	tests := map[string]string{
		"no header":            "",
		"basic scheme":         "Basic dXNlcjpwYXNz",
		"bearer without token": "Bearer ",
		"garbage token":        "Bearer garbage",
		"other secret":         "Bearer " + otherSigned.Token,
		"alg none":             "Bearer " + none,
		"expired":              "Bearer " + issuedNow.Token,
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next must not run") })
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/me/wallet", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()

			h.RequireAuth(next).ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			body, _ := io.ReadAll(rec.Body)
			assert.Contains(t, string(body), `"code":"UNAUTHENTICATED"`)
		})
	}
}

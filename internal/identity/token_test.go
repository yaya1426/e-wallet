package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-that-is-at-least-32-bytes!"

func TestTokens_IssueAndVerify(t *testing.T) {
	clk := newFakeClock()
	tk := tokens{secret: []byte(testSecret), ttl: 15 * time.Minute, clock: clk}
	userID := uuid.New()

	token, err := tk.issue(userID)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, token.ExpiresIn)

	var claims jwt.RegisteredClaims
	_, _, err = jwt.NewParser().ParseUnverified(token.Token, &claims)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.Equal(t, 15*time.Minute, claims.ExpiresAt.Sub(claims.IssuedAt.Time), "exp - iat = JWT_TTL")
	jti, err := uuid.Parse(claims.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), jti.Version())

	got, err := tk.verify(token.Token)
	require.NoError(t, err)
	assert.Equal(t, userID, got)
}

func TestTokens_VerifyRejects(t *testing.T) {
	clk := newFakeClock()
	tk := tokens{secret: []byte(testSecret), ttl: 15 * time.Minute, clock: clk}
	valid, err := tk.issue(uuid.New())
	require.NoError(t, err)

	other := tokens{secret: []byte(strings.Repeat("x", 40)), ttl: time.Minute, clock: clk}
	otherSigned, err := other.issue(uuid.New())
	require.NoError(t, err)

	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: uuid.New().String(), ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(clk.Now()),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	hs512, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.RegisteredClaims{
		Subject: uuid.New().String(), ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(clk.Now()),
	}).SignedString([]byte(testSecret))
	require.NoError(t, err)

	noExp, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: uuid.New().String()}).SignedString([]byte(testSecret))
	require.NoError(t, err)

	badSub, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: "not-a-uuid", ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(clk.Now()),
	}).SignedString([]byte(testSecret))
	require.NoError(t, err)

	for name, token := range map[string]string{
		"garbage": "not.a.jwt", "empty": "", "other secret": otherSigned.Token,
		"alg none": none, "alg HS512": hs512, "no exp": noExp, "sub not a uuid": badSub,
	} {
		_, err := tk.verify(token)
		assert.ErrorIs(t, err, ErrUnauthenticated, name)
	}

	clk.Advance(15*time.Minute + time.Second)
	_, err = tk.verify(valid.Token)
	assert.ErrorIs(t, err, ErrUnauthenticated, "expired")
}

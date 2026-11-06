package identity

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// tokens issues and verifies HS256 access tokens (TDR-011).
type tokens struct {
	secret []byte
	ttl    time.Duration
	clock  clock.Clock
}

// issue signs a token for userID with sub, iat, exp = iat + ttl and a UUIDv7 jti.
func (t tokens) issue(userID uuid.UUID) (AccessToken, error) {
	now := t.clock.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		ID:        id.New().String(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return AccessToken{}, fmt.Errorf("identity: sign token: %w", err)
	}
	return AccessToken{Token: signed, ExpiresIn: t.ttl}, nil
}

// verify checks the signature (HS256 only; any other alg is rejected), exp and iat, and
// returns the user ID in sub. Every failure wraps ErrUnauthenticated.
func (t tokens) verify(token string) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(t.clock.Now),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %w", ErrUnauthenticated, errors.Join(errors.New("bad sub"), err))
	}
	return userID, nil
}

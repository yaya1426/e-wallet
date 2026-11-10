package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/identity/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// ErrInvalidRefreshToken means the refresh token is unknown, expired, already used or revoked.
var ErrInvalidRefreshToken = errors.New("identity: invalid refresh token")

// refreshTokenBytes is the entropy of a refresh token (TDR-018 §3).
const refreshTokenBytes = 32

// Session is what a login or a refresh returns: an access token and a refresh token.
type Session struct {
	AccessToken
	RefreshToken     string
	RefreshExpiresIn time.Duration
}

// newRefreshToken returns a random opaque token and its SHA-256 (the only thing stored).
func newRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("identity: refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashRefreshToken(token), nil
}

func hashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// startSession issues an access token and the first refresh token of a new family.
func (s *Service) startSession(ctx context.Context, q Querier, userID uuid.UUID) (Session, error) {
	return s.issueSession(ctx, q, userID, id.New())
}

// issueSession issues an access token and a refresh token in familyID, valid refreshTTL.
func (s *Service) issueSession(ctx context.Context, q Querier, userID, familyID uuid.UUID) (Session, error) {
	access, err := s.tokens.issue(userID)
	if err != nil {
		return Session{}, err
	}
	token, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	if err := queries(q).InsertRefreshToken(ctx, sqlcgen.InsertRefreshTokenParams{
		ID: id.New(), UserID: userID, FamilyID: familyID, TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(s.refreshTTL),
	}); err != nil {
		return Session{}, fmt.Errorf("identity: store refresh token: %w", err)
	}
	return Session{AccessToken: access, RefreshToken: token, RefreshExpiresIn: s.refreshTTL}, nil
}

// Refresh exchanges a refresh token for a new session (rotation, TDR-018 §3): the token is
// marked used and a new one of the same family is issued, valid refreshTTL from now. A token
// that was already used or revoked revokes its whole family (it may have been stolen) and
// returns ErrInvalidRefreshToken; so do unknown and expired tokens. A disabled user gets
// ErrUserDisabled and the token stays valid.
func (s *Service) Refresh(ctx context.Context, token string) (Session, error) {
	hash := hashRefreshToken(token)
	var (
		session Session
		reused  *sqlcgen.RefreshToken
	)
	err := s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		session, reused = Session{}, nil
		q := queries(tx)
		row, err := q.GetRefreshTokenForUpdate(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidRefreshToken
		}
		if err != nil {
			return fmt.Errorf("identity: refresh lookup: %w", err)
		}
		now := s.now()
		if row.UsedAt != nil || row.RevokedAt != nil {
			if _, err := q.RevokeRefreshFamily(ctx, sqlcgen.RevokeRefreshFamilyParams{Now: now, FamilyID: row.FamilyID}); err != nil {
				return fmt.Errorf("identity: revoke family: %w", err)
			}
			reused = &row
			return nil // commit the revocation, then answer ErrInvalidRefreshToken
		}
		if !now.Before(row.ExpiresAt) {
			return ErrInvalidRefreshToken
		}
		user, err := q.GetUser(ctx, row.UserID)
		if err != nil {
			return fmt.Errorf("identity: refresh user: %w", err)
		}
		if Status(user.Status) == StatusDisabled {
			return ErrUserDisabled
		}
		if err := q.MarkRefreshTokenUsed(ctx, sqlcgen.MarkRefreshTokenUsedParams{UsedAt: now, ID: row.ID}); err != nil {
			return fmt.Errorf("identity: mark refresh token used: %w", err)
		}
		session, err = s.issueSession(ctx, tx, row.UserID, row.FamilyID)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if reused != nil {
		s.log.WarnContext(ctx, "refresh token reused; session revoked",
			slog.String("user_id", reused.UserID.String()), slog.String("family_id", reused.FamilyID.String()))
		return Session{}, ErrInvalidRefreshToken
	}
	return session, nil
}

// Logout revokes the session the refresh token belongs to, if it is userID's. Unknown tokens
// and other users' tokens change nothing (logout is idempotent and reveals nothing).
func (s *Service) Logout(ctx context.Context, userID uuid.UUID, token string) error {
	_, err := queries(s.db.Pool()).RevokeUserRefreshFamilyByToken(ctx, sqlcgen.RevokeUserRefreshFamilyByTokenParams{
		Now: s.now(), TokenHash: hashRefreshToken(token), UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("identity: logout: %w", err)
	}
	return nil
}

// RevokeAllSessions revokes every refresh token of the user, in q (e.g. after a password
// reset, T027). Access tokens already issued stay valid until they expire (≤ 15 min).
func (s *Service) RevokeAllSessions(ctx context.Context, q Querier, userID uuid.UUID) error {
	_, err := queries(q).RevokeUserRefreshTokens(ctx, sqlcgen.RevokeUserRefreshTokensParams{Now: s.now(), UserID: userID})
	if err != nil {
		return fmt.Errorf("identity: revoke sessions: %w", err)
	}
	return nil
}

func (s *Service) now() time.Time { return s.clock.Now().Truncate(time.Microsecond) }

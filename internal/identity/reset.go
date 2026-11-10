package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/yaya1426/e-wallet/internal/identity/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// Password reset rules (TDR-018 §4).
const (
	resetCodeTTL         = 15 * time.Minute
	resetCodeMaxAttempts = 5
)

// ErrInvalidResetCode means the phone, the code or its state (expired, used, too many wrong
// attempts) does not allow a reset. One error for all, so nothing is revealed.
var ErrInvalidResetCode = errors.New("identity: invalid reset code")

// ResetCode is a one-time code an operator gives a user out of band.
type ResetCode struct {
	Phone     string
	Code      string // 6 digits
	ExpiresAt time.Time
}

// IssueResetCode creates a 6-digit reset code for the user with phone, valid resetCodeTTL,
// and invalidates the user's earlier unused codes. An invalid phone returns ErrInvalidPhone,
// an unknown one ErrUserNotFound.
func (s *Service) IssueResetCode(ctx context.Context, phone string) (ResetCode, error) {
	if !ValidPhone(phone) {
		return ResetCode{}, ErrInvalidPhone
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return ResetCode{}, fmt.Errorf("identity: reset code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	var out ResetCode
	err = s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := queries(tx)
		user, err := q.GetUserByPhone(ctx, phone)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: reset user: %w", err)
		}
		now := s.now()
		if err := q.InvalidateResetCodes(ctx, sqlcgen.InvalidateResetCodesParams{Now: now, UserID: user.ID}); err != nil {
			return fmt.Errorf("identity: invalidate reset codes: %w", err)
		}
		out = ResetCode{Phone: phone, Code: code, ExpiresAt: now.Add(resetCodeTTL)}
		return q.InsertResetCode(ctx, sqlcgen.InsertResetCodeParams{
			ID: id.New(), UserID: user.ID, CodeHash: resetCodeHash(user.ID, code), CreatedAt: now, ExpiresAt: out.ExpiresAt,
		})
	})
	if err != nil {
		return ResetCode{}, err
	}
	s.log.InfoContext(ctx, "password reset code issued") // never the code or the phone
	return out, nil
}

// ResetPassword sets a new password with a code from IssueResetCode and signs the user out
// everywhere (RevokeAllSessions). An invalid new password returns ErrInvalidPassword without
// counting an attempt; anything wrong with the phone or the code returns ErrInvalidResetCode.
// A wrong code counts against the code; the fifth wrong attempt ends it.
func (s *Service) ResetPassword(ctx context.Context, phone, code, newPassword string) error {
	if n := len(newPassword); n < minPasswordBytes || n > maxPasswordBytes {
		return ErrInvalidPassword
	}
	if !ValidPhone(phone) {
		return ErrInvalidResetCode
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cost)
	if err != nil {
		return fmt.Errorf("identity: hash password: %w", err)
	}
	var wrong bool
	err = s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		wrong = false
		q := queries(tx)
		user, err := q.GetUserByPhone(ctx, phone)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidResetCode
		}
		if err != nil {
			return fmt.Errorf("identity: reset user: %w", err)
		}
		now := s.now()
		active, err := q.GetActiveResetCodeForUpdate(ctx, sqlcgen.GetActiveResetCodeForUpdateParams{UserID: user.ID, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidResetCode
		}
		if err != nil {
			return fmt.Errorf("identity: reset code lookup: %w", err)
		}
		if subtle.ConstantTimeCompare(active.CodeHash, resetCodeHash(user.ID, code)) != 1 {
			wrong = true
			return q.RecordWrongResetCode(ctx, sqlcgen.RecordWrongResetCodeParams{MaxAttempts: resetCodeMaxAttempts, Now: now, ID: active.ID})
		}
		if err := q.MarkResetCodeUsed(ctx, sqlcgen.MarkResetCodeUsedParams{Now: now, ID: active.ID}); err != nil {
			return fmt.Errorf("identity: use reset code: %w", err)
		}
		if err := q.UpdatePasswordHash(ctx, sqlcgen.UpdatePasswordHashParams{PasswordHash: string(hash), ID: user.ID}); err != nil {
			return fmt.Errorf("identity: update password: %w", err)
		}
		return s.RevokeAllSessions(ctx, tx, user.ID)
	})
	if err != nil {
		return err
	}
	if wrong { // the attempt was counted and committed
		return ErrInvalidResetCode
	}
	s.log.InfoContext(ctx, "password reset")
	return nil
}

// resetCodeHash binds the code to the user, so equal codes of two users differ.
func resetCodeHash(userID uuid.UUID, code string) []byte {
	sum := sha256.Sum256([]byte(userID.String() + ":" + code))
	return sum[:]
}

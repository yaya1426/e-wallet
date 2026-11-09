package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/yaya1426/e-wallet/internal/identity/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// DefaultBcryptCost is the password hashing cost (TDR-011).
const DefaultBcryptCost = 12

// WalletOpener opens a user's wallet inside the registration transaction. The wallet module
// implements it; identity depends only on this interface (architecture §3: identity → wallet).
type WalletOpener interface {
	Open(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error)
}

// Config configures the identity service.
type Config struct {
	JWTSecret  string        // HS256 secret, at least 32 bytes (checked by config.ValidateServe)
	JWTTTL     time.Duration // access token lifetime
	BcryptCost int           // 0 means DefaultBcryptCost; tests use bcrypt.MinCost
}

// Service registers users, logs them in and verifies access tokens.
type Service struct {
	db      *database.DB
	wallets WalletOpener
	tokens  tokens
	clock   clock.Clock
	cost    int
	// dummyHash is compared against for unknown phones so they take as long as a wrong
	// password (TDR-011, T004).
	dummyHash []byte
}

// NewService returns the identity service.
func NewService(db *database.DB, wallets WalletOpener, clk clock.Clock, cfg Config) (*Service, error) {
	cost := cfg.BcryptCost
	if cost == 0 {
		cost = DefaultBcryptCost
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), cost)
	if err != nil {
		return nil, fmt.Errorf("identity: dummy hash: %w", err)
	}
	return &Service{
		db:        db,
		wallets:   wallets,
		tokens:    tokens{secret: []byte(cfg.JWTSecret), ttl: cfg.JWTTTL, clock: clk},
		clock:     clk,
		cost:      cost,
		dummyHash: dummyHash,
	}, nil
}

// Register validates the input, then creates the user and their wallet (with its ledger
// account) in one DB transaction (PRD-001 US-1, BR-5). The password is hashed before the
// transaction so a retry does not hash again.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Registration, error) {
	in, err := in.validate()
	if err != nil {
		return Registration{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cost)
	if err != nil {
		return Registration{}, fmt.Errorf("identity: hash password: %w", err)
	}

	var reg Registration
	err = s.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// PostgreSQL stores microseconds; truncate so the 201 body equals later reads.
		user := User{ID: id.New(), Phone: in.Phone, FullName: in.FullName, Status: StatusActive,
			CreatedAt: s.clock.Now().Truncate(time.Microsecond)}
		err := queries(tx).CreateUser(ctx, sqlcgen.CreateUserParams{
			ID:           user.ID,
			PhoneE164:    user.Phone,
			FullName:     user.FullName,
			PasswordHash: string(hash),
			CreatedAt:    user.CreatedAt,
		})
		if violates(err, codeUniqueViolation, constraintPhoneUnique) {
			return ErrPhoneTaken
		}
		if err != nil {
			return fmt.Errorf("identity: insert user: %w", err)
		}
		walletID, err := s.wallets.Open(ctx, tx, user.ID)
		if err != nil {
			return fmt.Errorf("identity: open wallet: %w", err)
		}
		reg = Registration{User: user, WalletID: walletID}
		return nil
	})
	if err != nil {
		return Registration{}, err
	}
	return reg, nil
}

// Login checks the credentials and returns an access token (PRD-001 US-2). An unknown phone
// and a wrong password both return ErrInvalidCredentials after the same bcrypt work; a
// disabled user with the right password returns ErrUserDisabled.
func (s *Service) Login(ctx context.Context, phone, password string) (AccessToken, error) {
	// A malformed phone matches no user, and bcrypt would ignore bytes past 72, letting
	// "password + any suffix" pass (review T004 #2). Both fail after the same bcrypt work.
	if !egyptianMobile.MatchString(phone) || len(password) > maxPasswordBytes {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return AccessToken{}, ErrInvalidCredentials
	}
	row, err := queries(s.db.Pool()).GetUserByPhone(ctx, phone)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return AccessToken{}, ErrInvalidCredentials
	}
	if err != nil {
		return AccessToken{}, fmt.Errorf("identity: login lookup: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)); err != nil {
		return AccessToken{}, ErrInvalidCredentials
	}
	if Status(row.Status) == StatusDisabled {
		return AccessToken{}, ErrUserDisabled
	}
	return s.tokens.issue(row.ID)
}

// Authenticate verifies an access token and returns its user's ID. An invalid token or an
// unknown user returns ErrUnauthenticated; a disabled user returns ErrUserDisabled.
func (s *Service) Authenticate(ctx context.Context, token string) (uuid.UUID, error) {
	userID, err := s.tokens.verify(token)
	if err != nil {
		return uuid.Nil, err
	}
	user, err := s.GetUser(ctx, userID)
	if errors.Is(err, ErrUserNotFound) {
		return uuid.Nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if user.Status == StatusDisabled {
		return uuid.Nil, ErrUserDisabled
	}
	return userID, nil
}

// PreviewRecipient lets callerID confirm who a phone belongs to before paying (PRD-004 Q7):
// it returns the masked name only. A malformed phone returns ErrInvalidPhone, an unknown one
// ErrRecipientNotFound, the caller's own ErrSelfRecipient. Disabled users are shown like any
// other (T009).
func (s *Service) PreviewRecipient(ctx context.Context, callerID uuid.UUID, phone string) (Recipient, error) {
	if !ValidPhone(phone) {
		return Recipient{}, ErrInvalidPhone
	}
	user, err := s.GetUserByPhone(ctx, s.db.Pool(), phone)
	if errors.Is(err, ErrUserNotFound) {
		return Recipient{}, ErrRecipientNotFound
	}
	if err != nil {
		return Recipient{}, err
	}
	if user.ID == callerID {
		return Recipient{}, ErrSelfRecipient
	}
	return Recipient{Phone: user.Phone, MaskedName: MaskName(user.FullName)}, nil
}

// GetUser returns the user with the given ID.
func (s *Service) GetUser(ctx context.Context, userID uuid.UUID) (User, error) {
	row, err := queries(s.db.Pool()).GetUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("%w: %s", ErrUserNotFound, userID)
	}
	if err != nil {
		return User{}, fmt.Errorf("identity: get user %s: %w", userID, err)
	}
	return toUser(row), nil
}

// GetUserByPhone returns the user with the given E.164 phone. q is a pgx.Tx (e.g. inside a
// transfer's unit of work, so it does not need a second pool connection) or the pool.
func (s *Service) GetUserByPhone(ctx context.Context, q Querier, phone string) (User, error) {
	row, err := queries(q).GetUserByPhone(ctx, phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound // the phone is personal data; keep it out of error text
	}
	if err != nil {
		return User{}, fmt.Errorf("identity: get user by phone: %w", err)
	}
	return toUser(row), nil
}

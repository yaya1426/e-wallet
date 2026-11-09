// Package identity owns users, registration, login and access tokens (TDR-011).
package identity

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Status of a user.
type Status string

// User statuses.
const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
)

// User is a registered user. The password hash never leaves the module.
type User struct {
	ID        uuid.UUID
	Phone     string // E.164, e.g. +201001234567
	FullName  string
	Status    Status
	CreatedAt time.Time
}

// RegisterInput is a registration request.
type RegisterInput struct {
	Phone    string
	FullName string
	Password string
}

// Registration is the result of a successful registration.
type Registration struct {
	User     User
	WalletID uuid.UUID
}

// Recipient is what another user may see of a user before paying them (PRD-004 P2P-BR-5).
type Recipient struct {
	Phone      string
	MaskedName string
}

// AccessToken is a signed JWT and how long it is valid.
type AccessToken struct {
	Token     string
	ExpiresIn time.Duration
}

// Validation limits (T004 decisions).
const (
	minPasswordBytes = 8
	maxPasswordBytes = 72 // bcrypt ignores anything longer
	minNameRunes     = 2
	maxNameRunes     = 100
)

// egyptianMobile is an Egyptian mobile number in E.164: +20, then 10, 11, 12 or 15, then 8 digits.
var egyptianMobile = regexp.MustCompile(`^\+201[0125][0-9]{8}$`)

var (
	// ErrInvalidPhone means the phone is not an Egyptian mobile number in E.164.
	ErrInvalidPhone = errors.New("identity: invalid phone")
	// ErrInvalidPassword means the password is shorter than 8 or longer than 72 bytes.
	ErrInvalidPassword = errors.New("identity: invalid password")
	// ErrInvalidName means the trimmed full name is shorter than 2 or longer than 100 characters.
	ErrInvalidName = errors.New("identity: invalid name")
	// ErrPhoneTaken means the phone is already registered.
	ErrPhoneTaken = errors.New("identity: phone already registered")
	// ErrInvalidCredentials means the phone is unknown or the password is wrong.
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
	// ErrUserDisabled means the user exists but is disabled.
	ErrUserDisabled = errors.New("identity: user disabled")
	// ErrUserNotFound means no user has the given ID or phone.
	ErrUserNotFound = errors.New("identity: user not found")
	// ErrRecipientNotFound means no user is registered with the recipient phone.
	ErrRecipientNotFound = errors.New("identity: recipient not found")
	// ErrSelfRecipient means the recipient phone is the caller's own.
	ErrSelfRecipient = errors.New("identity: recipient is the caller")
	// ErrUnauthenticated means the access token is missing, malformed, badly signed or expired.
	ErrUnauthenticated = errors.New("identity: unauthenticated")
)

// ValidPhone reports whether phone is an Egyptian mobile number in E.164 (T004 rule).
func ValidPhone(phone string) bool { return egyptianMobile.MatchString(phone) }

// validate checks the input in the order of the API contract: phone, password, name. It
// returns the input with the name trimmed.
func (in RegisterInput) validate() (RegisterInput, error) {
	if !egyptianMobile.MatchString(in.Phone) {
		return in, ErrInvalidPhone
	}
	if n := len(in.Password); n < minPasswordBytes || n > maxPasswordBytes {
		return in, ErrInvalidPassword
	}
	in.FullName = strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(in.FullName); n < minNameRunes || n > maxNameRunes {
		return in, ErrInvalidName
	}
	return in, nil
}

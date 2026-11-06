// Package money represents amounts as integer minor units (piasters for EGP). Floating
// point is never used (TDR-003).
package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Currency is an ISO 4217 currency code. EGP is the only supported currency (PRD-001 BR-1).
type Currency string

// EGP is the Egyptian pound; its minor unit is the piaster (1 EGP = 100 piasters).
const EGP Currency = "EGP"

// minorPerMajor is the number of minor units in one major unit of EGP (ISO 4217 exponent 2).
const minorPerMajor = 100

var (
	// ErrUnsupportedCurrency means the currency code is not EGP.
	ErrUnsupportedCurrency = errors.New("money: unsupported currency")
	// ErrNotPositive means a movement amount is zero or negative (PRD-001 BR-4).
	ErrNotPositive = errors.New("money: amount must be positive")
	// ErrCurrencyMismatch means two amounts in different currencies were combined.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow means the result does not fit in int64.
	ErrOverflow = errors.New("money: amount overflow")
)

// ParseCurrency returns the currency for code. Only the exact code "EGP" is accepted.
func ParseCurrency(code string) (Currency, error) {
	if Currency(code) != EGP {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedCurrency, code)
	}
	return EGP, nil
}

// Money is an amount in minor units of a currency. Its fields are unexported, so a valid
// value only comes from New or NewPositive. The zero value has no currency and is not a
// valid amount.
type Money struct {
	amount   int64
	currency Currency
}

// New returns an amount of any sign (balances may be negative for system accounts, TDR-010 §3).
func New(amount int64, currency Currency) (Money, error) {
	if _, err := ParseCurrency(string(currency)); err != nil {
		return Money{}, err
	}
	return Money{amount: amount, currency: currency}, nil
}

// NewPositive returns a movement amount, which must be greater than zero (PRD-001 BR-4).
func NewPositive(amount int64, currency Currency) (Money, error) {
	m, err := New(amount, currency)
	if err != nil {
		return Money{}, err
	}
	if amount <= 0 {
		return Money{}, fmt.Errorf("%w: %d", ErrNotPositive, amount)
	}
	return m, nil
}

// Amount returns the amount in minor units.
func (m Money) Amount() int64 { return m.amount }

// Currency returns the currency.
func (m Money) Currency() Currency { return m.currency }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.amount == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.amount < 0 }

// Add returns m + o. It fails instead of wrapping around on overflow.
func (m Money) Add(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	if (o.amount > 0 && m.amount > math.MaxInt64-o.amount) ||
		(o.amount < 0 && m.amount < math.MinInt64-o.amount) {
		return Money{}, fmt.Errorf("%w: %d + %d", ErrOverflow, m.amount, o.amount)
	}
	return Money{amount: m.amount + o.amount, currency: m.currency}, nil
}

// Sub returns m - o. It fails instead of wrapping around on overflow.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	if (o.amount < 0 && m.amount > math.MaxInt64+o.amount) ||
		(o.amount > 0 && m.amount < math.MinInt64+o.amount) {
		return Money{}, fmt.Errorf("%w: %d - %d", ErrOverflow, m.amount, o.amount)
	}
	return Money{amount: m.amount - o.amount, currency: m.currency}, nil
}

// Format renders the amount for display, e.g. "150.75 EGP" or "-0.05 EGP", with integer
// arithmetic only.
func (m Money) Format() string {
	// Divide before negating: |MinInt64 / 100| and |MinInt64 % 100| both fit in int64.
	major, minor := m.amount/minorPerMajor, m.amount%minorPerMajor
	sign := ""
	if m.amount < 0 {
		sign, major, minor = "-", -major, -minor
	}
	frac := strconv.FormatInt(minor, 10)
	if minor < 10 {
		frac = "0" + frac
	}
	return sign + strconv.FormatInt(major, 10) + "." + frac + " " + string(m.currency)
}

// String returns Format, for logs.
func (m Money) String() string { return m.Format() }

func (m Money) sameCurrency(o Money) error {
	if m.currency == "" || m.currency != o.currency {
		return fmt.Errorf("%w: %q and %q", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}

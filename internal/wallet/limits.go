package wallet

import (
	"errors"
	"fmt"
)

// Limits are the money limits of PRD-001 §5.6, in piasters. They come from configuration
// (LIMIT_*); the checks are pure so every flow applies them the same way. Callers read the
// balances and sums they pass in under the wallet's ledger lock.
type Limits struct {
	TopUpMin, TopUpMax           int64
	WithdrawalMin, WithdrawalMax int64
	P2PMin, P2PMax               int64
	MaxBalance                   int64 // largest balance a wallet may reach
	DailyOutgoing                int64 // withdrawals + P2P sent per Africa/Cairo day
}

var (
	// ErrAmountOutOfRange means the amount is outside the flow's [min, max].
	ErrAmountOutOfRange = errors.New("wallet: amount out of range")
	// ErrDailyLimitExceeded means the amount would exceed today's outgoing limit.
	ErrDailyLimitExceeded = errors.New("wallet: daily outgoing limit exceeded")
	// ErrRecipientLimitExceeded means the recipient's balance would exceed the maximum.
	ErrRecipientLimitExceeded = errors.New("wallet: recipient balance limit exceeded")
	// ErrWalletLimitExceeded means the wallet's balance plus pending top-ups would exceed the maximum.
	ErrWalletLimitExceeded = errors.New("wallet: balance limit exceeded")
)

// CheckP2PAmount checks a transfer amount against [P2PMin, P2PMax].
func (l Limits) CheckP2PAmount(amount int64) error { return inRange(amount, l.P2PMin, l.P2PMax) }

// CheckTopUpAmount checks a top-up amount against [TopUpMin, TopUpMax].
func (l Limits) CheckTopUpAmount(amount int64) error { return inRange(amount, l.TopUpMin, l.TopUpMax) }

// CheckWithdrawalAmount checks a withdrawal amount against [WithdrawalMin, WithdrawalMax].
func (l Limits) CheckWithdrawalAmount(amount int64) error {
	return inRange(amount, l.WithdrawalMin, l.WithdrawalMax)
}

// CheckDailyOutgoing checks that spentToday + amount stays within DailyOutgoing.
func (l Limits) CheckDailyOutgoing(spentToday, amount int64) error {
	if exceeds(spentToday, amount, l.DailyOutgoing) {
		return fmt.Errorf("%w: spent %d + %d > %d", ErrDailyLimitExceeded, spentToday, amount, l.DailyOutgoing)
	}
	return nil
}

// CheckRecipientBalance checks that a P2P recipient's balance + amount stays within MaxBalance.
func (l Limits) CheckRecipientBalance(balance, amount int64) error {
	if exceeds(balance, amount, l.MaxBalance) {
		return ErrRecipientLimitExceeded
	}
	return nil
}

// CheckTopUpBalance checks that balance + pending top-ups + amount stays within MaxBalance
// (PRD-002 §3.5).
func (l Limits) CheckTopUpBalance(balance, pendingTopUps, amount int64) error {
	if exceeds(balance, pendingTopUps, l.MaxBalance) || exceeds(balance+pendingTopUps, amount, l.MaxBalance) {
		return ErrWalletLimitExceeded
	}
	return nil
}

func inRange(amount, lo, hi int64) error {
	if amount < lo || amount > hi {
		return fmt.Errorf("%w: %d not in [%d, %d]", ErrAmountOutOfRange, amount, lo, hi)
	}
	return nil
}

// exceeds reports whether current + amount > limit without overflowing int64 (all values
// are non-negative piasters).
func exceeds(current, amount, limit int64) bool {
	return amount > limit-current
}

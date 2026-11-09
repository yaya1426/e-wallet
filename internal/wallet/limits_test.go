package wallet

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

var testLimits = Limits{
	TopUpMin: 1000, TopUpMax: 3_000_000,
	WithdrawalMin: 1000, WithdrawalMax: 3_000_000,
	P2PMin: 100, P2PMax: 3_000_000,
	MaxBalance: 10_000_000, DailyOutgoing: 6_000_000,
}

func TestLimits_Ranges(t *testing.T) {
	assert.ErrorIs(t, testLimits.CheckP2PAmount(99), ErrAmountOutOfRange)
	assert.NoError(t, testLimits.CheckP2PAmount(100))
	assert.NoError(t, testLimits.CheckP2PAmount(3_000_000))
	assert.ErrorIs(t, testLimits.CheckP2PAmount(3_000_001), ErrAmountOutOfRange)
	assert.ErrorIs(t, testLimits.CheckTopUpAmount(999), ErrAmountOutOfRange)
	assert.NoError(t, testLimits.CheckTopUpAmount(1000))
	assert.ErrorIs(t, testLimits.CheckWithdrawalAmount(3_000_001), ErrAmountOutOfRange)
	assert.NoError(t, testLimits.CheckWithdrawalAmount(3_000_000))
}

func TestLimits_Daily(t *testing.T) {
	assert.NoError(t, testLimits.CheckDailyOutgoing(5_000_000, 1_000_000), "exactly the limit")
	assert.ErrorIs(t, testLimits.CheckDailyOutgoing(5_000_000, 1_000_001), ErrDailyLimitExceeded)
	assert.ErrorIs(t, testLimits.CheckDailyOutgoing(1, math.MaxInt64), ErrDailyLimitExceeded, "no overflow")
}

func TestLimits_Balances(t *testing.T) {
	assert.NoError(t, testLimits.CheckRecipientBalance(9_000_000, 1_000_000))
	assert.ErrorIs(t, testLimits.CheckRecipientBalance(9_000_000, 1_000_001), ErrRecipientLimitExceeded)
	assert.ErrorIs(t, testLimits.CheckRecipientBalance(1, math.MaxInt64), ErrRecipientLimitExceeded, "no overflow")

	assert.NoError(t, testLimits.CheckTopUpBalance(8_000_000, 1_000_000, 1_000_000))
	assert.ErrorIs(t, testLimits.CheckTopUpBalance(8_000_000, 1_000_000, 1_000_001), ErrWalletLimitExceeded, "pending top-ups count")
	assert.ErrorIs(t, testLimits.CheckTopUpBalance(1, math.MaxInt64, 1), ErrWalletLimitExceeded, "no overflow")
}

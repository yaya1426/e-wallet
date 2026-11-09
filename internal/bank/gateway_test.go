package bank_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yaya1426/e-wallet/internal/bank"
)

func TestRejectedError(t *testing.T) {
	err := fmt.Errorf("create payout: %w", &bank.RejectedError{Code: "INVALID_ACCOUNT", Message: "IBAN does not exist"})

	var rejected *bank.RejectedError
	assert.True(t, errors.As(err, &rejected))
	assert.Equal(t, "INVALID_ACCOUNT", rejected.Code)
	assert.EqualError(t, rejected, "bank: rejected: INVALID_ACCOUNT: IBAN does not exist")
	assert.NotErrorIs(t, err, bank.ErrTemporary)
}

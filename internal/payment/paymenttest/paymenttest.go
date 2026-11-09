// Package paymenttest holds test helpers for the payment flows.
package paymenttest

import (
	"context"
	"testing"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
)

// AssertInTransit checks data-model invariant 5: the balance of WITHDRAWALS_IN_TRANSIT equals
// the sum of the withdrawals still waiting for the bank (PENDING or PROCESSING). Every such
// withdrawal is reserved; every finished one is settled or refunded.
func AssertInTransit(t testing.TB, db *database.DB) {
	t.Helper()
	ctx := context.Background()
	account, err := ledger.NewService(db, clock.System{}).GetAccountByCode(ctx, db.Pool(), ledger.CodeWithdrawalsInTransit)
	if err != nil {
		t.Fatalf("paymenttest: read %s: %v", ledger.CodeWithdrawalsInTransit, err)
	}
	var inFlight int64
	if err := db.Pool().QueryRow(ctx, `SELECT coalesce(sum(amount), 0)::bigint FROM transactions
		WHERE type = 'WITHDRAWAL' AND status IN ('PENDING', 'PROCESSING')`).Scan(&inFlight); err != nil {
		t.Fatalf("paymenttest: sum withdrawals in flight: %v", err)
	}
	if got := account.Balance.Amount(); got != inFlight {
		t.Errorf("paymenttest: %s balance is %d, withdrawals in flight sum to %d", ledger.CodeWithdrawalsInTransit, got, inFlight)
	}
}

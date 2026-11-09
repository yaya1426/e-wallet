// Package paymenttest holds test helpers for the payment flows.
package paymenttest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AssertInTransit checks data-model invariant 5: the balance of WITHDRAWALS_IN_TRANSIT equals
// the sum of the withdrawals still waiting for the bank (PENDING or PROCESSING). Every such
// withdrawal is reserved; every finished one is settled or refunded. Both sides are read in
// one statement (one snapshot), so the check holds while other requests run.
func AssertInTransit(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	var balance, inFlight int64
	err := pool.QueryRow(context.Background(), `SELECT
		(SELECT balance FROM ledger_accounts WHERE code = 'WITHDRAWALS_IN_TRANSIT'),
		(SELECT coalesce(sum(amount), 0)::bigint FROM transactions
		  WHERE type = 'WITHDRAWAL' AND status IN ('PENDING', 'PROCESSING'))`).Scan(&balance, &inFlight)
	if err != nil {
		t.Fatalf("paymenttest: read the in-transit invariant: %v", err)
	}
	if balance != inFlight {
		t.Errorf("paymenttest: WITHDRAWALS_IN_TRANSIT balance is %d, withdrawals in flight sum to %d", balance, inFlight)
	}
}

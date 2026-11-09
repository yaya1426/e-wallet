//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/payment/paymenttest"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
)

// databaseURL is the compose database as seen from the host (E2E_DATABASE_URL overrides it,
// e.g. when POSTGRES_HOST_PORT is not 5432).
var databaseURL = baseURL("E2E_DATABASE_URL", "postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable")

var (
	dbOnce sync.Once
	dbConn *database.DB
	dbErr  error
)

// stackDB opens one connection pool to the compose database for the whole run.
func stackDB(t *testing.T) *database.DB {
	t.Helper()
	dbOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dbConn, dbErr = database.Open(ctx, databaseURL, database.Options{Tx: database.DefaultTxSettings()})
	})
	require.NoError(t, dbErr, "connect to %s (set E2E_DATABASE_URL)", databaseURL)
	return dbConn
}

func pool(t *testing.T) *pgxpool.Pool { return stackDB(t).Pool() }

// checkIntegrity runs the ledger integrity check (TDR-004 §6: entries balance, cached
// balances match the postings) and the in-transit invariant (data-model invariant 5). The
// stack's background work (outbox, inbox) keeps running, so it waits for the work of this
// test to finish first.
func checkIntegrity(t *testing.T) {
	t.Helper()
	db := stackDB(t)
	waitQuiet(t)
	report, err := ledger.NewService(db, clock.System{}).VerifyIntegrity(context.Background())
	require.NoError(t, err)
	if !report.OK() {
		t.Errorf("ledger integrity broken: %+v", report)
	}
	paymenttest.AssertInTransit(t, db.Pool())
}

// waitQuiet waits until no outbox or inbox event is pending (a failing or payout-retrying
// event of another test would show up here as a timeout, which is a real problem too).
func waitQuiet(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(finalTimeout)
	for {
		var pending int
		require.NoError(t, pool(t).QueryRow(context.Background(), `SELECT
			(SELECT count(*) FROM outbox_events WHERE status IN ('PENDING', 'PROCESSING')) +
			(SELECT count(*) FROM inbox_events WHERE status IN ('PENDING', 'PROCESSING'))`).Scan(&pending))
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%d outbox/inbox events still pending after %s", pending, finalTimeout)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

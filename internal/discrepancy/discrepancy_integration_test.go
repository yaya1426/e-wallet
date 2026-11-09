//go:build integration

package discrepancy_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

type fixture struct {
	db    *database.DB
	clock *clocktest.Fake
	svc   *discrepancy.Service
	txn   uuid.UUID // an existing transaction (the foreign key needs one)
	txn2  uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	require.NoError(t, migrator.Up(ctx))

	clk := clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	wallets := wallet.NewService(db, ledger.NewService(db, clk), clk)
	txns := transaction.NewService(clk)
	// The foreign key needs real transactions: a user row (identity is not needed here), a
	// wallet and a PENDING top-up.
	newTopUp := func(phone string) uuid.UUID {
		var txnID uuid.UUID
		require.NoError(t, db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			userID := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, phone_e164, full_name, password_hash, created_at)
				VALUES ($1, $2, 'Test User', 'x', now())`, userID, phone); err != nil {
				return err
			}
			walletID, err := wallets.Open(ctx, tx, userID)
			if err != nil {
				return err
			}
			amount, err := money.NewPositive(100, money.EGP)
			if err != nil {
				return err
			}
			txn, err := txns.Create(ctx, tx, transaction.New{
				Type: transaction.TypeTopUp, Status: transaction.StatusPending, InitiatorUserID: userID,
				DestinationWalletID: walletID, Amount: amount, IdempotencyKey: uuid.NewString(),
			})
			txnID = txn.ID
			return err
		}))
		return txnID
	}
	return fixture{db: db, clock: clk, svc: discrepancy.NewService(db.Pool(), clk, nil),
		txn: newTopUp("+201000000001"), txn2: newTopUp("+201000000002")}
}

func (f fixture) record(t *testing.T, d discrepancy.New) {
	t.Helper()
	require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return f.svc.Record(ctx, tx, d)
	}))
	f.clock.Advance(time.Second)
}

// AC-10.
func TestDiscrepancy_RecordDedupAndList(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	f.record(t, discrepancy.New{Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityCritical,
		TransactionID: f.txn, Details: map[string]any{"reason": "first"}})
	f.record(t, discrepancy.New{Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityCritical,
		TransactionID: f.txn, Details: map[string]any{"reason": "again"}}) // same open problem: no new row
	f.record(t, discrepancy.New{Kind: discrepancy.KindAmountMismatch, Severity: discrepancy.SeverityCritical,
		TransactionID: f.txn, Details: map[string]any{"expected": 100}}) // another kind: new row
	f.record(t, discrepancy.New{Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityCritical,
		TransactionID: f.txn2}) // another transaction: new row, nil details → {}
	f.record(t, discrepancy.New{Kind: discrepancy.KindUnknownBankEvent, Severity: discrepancy.SeverityWarning,
		Details: map[string]any{"event_id": "e-1"}})
	f.record(t, discrepancy.New{Kind: discrepancy.KindUnknownBankEvent, Severity: discrepancy.SeverityWarning,
		Details: map[string]any{"event_id": "e-2"}}) // no transaction: never deduplicated

	all, err := f.svc.List(ctx, discrepancy.Filter{})
	require.NoError(t, err)
	require.Len(t, all, 5)
	for i := 1; i < len(all); i++ {
		assert.True(t, all[i-1].CreatedAt.After(all[i].CreatedAt), "newest first")
	}
	assert.Equal(t, discrepancy.KindUnknownBankEvent, all[0].Kind)
	assert.JSONEq(t, `{"event_id":"e-2"}`, string(all[0].Details))
	assert.Equal(t, uuid.Nil, all[0].TransactionID)
	assert.Equal(t, discrepancy.SeverityWarning, all[0].Severity)
	assert.Equal(t, discrepancy.StatusOpen, all[0].Status)
	assert.JSONEq(t, `{}`, string(all[2].Details))
	assert.Equal(t, f.txn2, all[2].TransactionID)
	first := all[4]
	assert.Equal(t, f.txn, first.TransactionID)
	assert.Equal(t, discrepancy.KindStatusMismatch, first.Kind)
	var details map[string]any
	require.NoError(t, json.Unmarshal(first.Details, &details))
	assert.Equal(t, "first", details["reason"], "the first record is kept")

	// Resolve the first one (phase 2 does it through an API): it leaves the OPEN filter, and
	// the same problem can be opened again.
	_, err = f.db.Pool().Exec(ctx, `UPDATE reconciliation_discrepancies SET status = 'RESOLVED', resolved_at = now(),
		resolution_note = 'checked with the bank' WHERE id = $1`, first.ID)
	require.NoError(t, err)
	open, err := f.svc.List(ctx, discrepancy.Filter{Status: discrepancy.StatusOpen})
	require.NoError(t, err)
	assert.Len(t, open, 4)
	resolved, err := f.svc.List(ctx, discrepancy.Filter{Status: discrepancy.StatusResolved})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, "checked with the bank", resolved[0].ResolutionNote)
	assert.NotNil(t, resolved[0].ResolvedAt)

	f.record(t, discrepancy.New{Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityCritical, TransactionID: f.txn})
	open, err = f.svc.List(ctx, discrepancy.Filter{Status: discrepancy.StatusOpen, Limit: 2})
	require.NoError(t, err)
	require.Len(t, open, 2, "limit")
	assert.Equal(t, f.txn, open[0].TransactionID, "re-opened after the resolution")
}

func TestDiscrepancy_RecordedOnlyIfTheCallerCommits(t *testing.T) {
	f := setup(t)
	errRollback := assert.AnError
	err := f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		require.NoError(t, f.svc.Record(ctx, tx, discrepancy.New{Kind: discrepancy.KindStatusMismatch,
			Severity: discrepancy.SeverityCritical, TransactionID: f.txn}))
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	all, err := f.svc.List(context.Background(), discrepancy.Filter{})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDiscrepancy_Invalid(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, d := range []discrepancy.New{
		{Kind: "OOPS", Severity: discrepancy.SeverityCritical},
		{Kind: discrepancy.KindStatusMismatch, Severity: "LOW"},
		{Kind: discrepancy.KindStatusMismatch, Severity: discrepancy.SeverityWarning, Details: func() {}},
	} {
		err := f.db.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return f.svc.Record(ctx, tx, d) })
		require.Error(t, err)
	}
	for _, filter := range []discrepancy.Filter{{Limit: -1}, {Limit: discrepancy.MaxListLimit + 1}, {Status: "CLOSED"}} {
		_, err := f.svc.List(ctx, filter)
		require.ErrorIs(t, err, discrepancy.ErrInvalid)
	}
}

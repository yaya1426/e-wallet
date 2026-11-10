//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
)

// T028 AC-1: the ops summary and its status rules.
func TestInternal_OpsHealth(t *testing.T) {
	f, clk := newOperatorFixture(t)
	get := func() map[string]any {
		t.Helper()
		r := f.internal(t, http.MethodGet, "/internal/v1/health/ops", testInternalKey, "")
		require.Equal(t, http.StatusOK, r.status, r.body)
		return r.body
	}
	assert.Equal(t, http.StatusUnauthorized, f.internal(t, http.MethodGet, "/internal/v1/health/ops", "", "").status)

	body := get()
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, map[string]any{"open_critical": 0.0, "open_warning": 0.0}, body["discrepancies"])
	assert.Equal(t, map[string]any{"TOPUP": 0.0, "WITHDRAWAL": 0.0}, body["stuck"])
	assert.Equal(t, map[string]any{"pending": 0.0, "dead": 0.0}, body["outbox"])
	assert.Equal(t, map[string]any{"pending": 0.0, "dead": 0.0}, body["inbox"])
	jobs := body["jobs"].([]any)
	require.Len(t, jobs, 3)
	first := jobs[0].(map[string]any)
	assert.Equal(t, "pending_bank_ops", first["name"])
	assert.Nil(t, first["last_status"])
	assert.Equal(t, false, first["late"], "just started")

	// A failed job run → warning.
	_, err := f.db.Pool().Exec(context.Background(), `INSERT INTO job_runs (id, job_name, started_at, finished_at, status, error)
		VALUES (gen_random_uuid(), 'pending_bank_ops', $1, $1, 'FAILED', 'bank down')`, clk.Now())
	require.NoError(t, err)
	body = get()
	assert.Equal(t, "warning", body["status"])
	assert.Equal(t, "FAILED", body["jobs"].([]any)[0].(map[string]any)["last_status"])
	_, err = f.db.Pool().Exec(context.Background(), `INSERT INTO job_runs (id, job_name, started_at, finished_at, status)
		VALUES (gen_random_uuid(), 'pending_bank_ops', $1, $1, 'SUCCESS')`, clk.Now().Add(time.Second))
	require.NoError(t, err)
	clk.Advance(2 * time.Second)

	// Late jobs → warning (the others never ran within 3 intervals of start-up).
	clk.Advance(46 * time.Minute) // > 3 × 15 min (stuck_alert), so it is late
	body = get()
	assert.Equal(t, "warning", body["status"])

	// An open WARNING discrepancy keeps warning; a CRITICAL one → critical.
	svc := discrepancy.NewService(f.db.Pool(), clk, nil)
	record := func(sev discrepancy.Severity, kind discrepancy.Kind) {
		require.NoError(t, f.db.InTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			return svc.Record(ctx, tx, discrepancy.New{Kind: kind, Severity: sev})
		}))
	}
	record(discrepancy.SeverityWarning, discrepancy.KindUnknownBankEvent)
	body = get()
	assert.Equal(t, "warning", body["status"])
	assert.EqualValues(t, 1, body["discrepancies"].(map[string]any)["open_warning"])
	record(discrepancy.SeverityCritical, discrepancy.KindLedgerIntegrity)
	body = get()
	assert.Equal(t, "critical", body["status"])
	assert.EqualValues(t, 1, body["discrepancies"].(map[string]any)["open_critical"])
}

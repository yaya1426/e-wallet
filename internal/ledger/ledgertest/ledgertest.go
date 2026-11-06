// Package ledgertest provides ledger assertions for tests in any package (PRD-001 §9.3:
// the integrity check passes after every test scenario).
package ledgertest

import (
	"context"
	"testing"

	"github.com/yaya1426/e-wallet/internal/ledger"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/database"
)

// AssertIntegrity fails the test if the committed ledger in db violates an invariant
// (ledger.Service.VerifyIntegrity) and reports every violation found.
func AssertIntegrity(t testing.TB, db *database.DB) {
	t.Helper()
	report, err := ledger.NewService(db, clock.System{}).VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("ledgertest: verify integrity: %v", err)
	}
	if report.OK() {
		return
	}
	if report.TotalDebits != report.TotalCredits {
		t.Errorf("ledgertest: total debits %d != total credits %d", report.TotalDebits, report.TotalCredits)
	}
	for _, d := range report.Drifts {
		t.Errorf("ledgertest: account %s (%s) cached balance %d != computed %d", d.AccountID, d.Code, d.Cached, d.Computed)
	}
	for _, u := range report.UnbalancedEntries {
		t.Errorf("ledgertest: entry %s unbalanced: debits %d, credits %d, postings %d", u.EntryID, u.Debits, u.Credits, u.Postings)
	}
}

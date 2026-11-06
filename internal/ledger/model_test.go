package ledger

import (
	"math"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/money"
)

func egp(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.New(amount, money.EGP)
	require.NoError(t, err)
	return m
}

func TestEntryValidate(t *testing.T) {
	a, b, c := AccountID(uuid.New()), AccountID(uuid.New()), AccountID(uuid.New())
	entry := func(postings ...Posting) Entry {
		return Entry{TransactionID: uuid.New(), Type: EntryP2P, Postings: postings}
	}
	tests := []struct {
		name    string
		entry   Entry
		wantErr error
	}{
		{"balanced pair", entry(Posting{a, Debit, egp(t, 1000)}, Posting{b, Credit, egp(t, 1000)}), nil},
		{"balanced split", entry(Posting{a, Debit, egp(t, 1000)}, Posting{b, Credit, egp(t, 600)}, Posting{c, Credit, egp(t, 400)}), nil},
		{"no postings", entry(), ErrTooFewPostings},
		{"one posting", entry(Posting{a, Debit, egp(t, 1000)}), ErrTooFewPostings},
		{"unbalanced", entry(Posting{a, Debit, egp(t, 1000)}, Posting{b, Credit, egp(t, 999)}), ErrUnbalanced},
		{"only debits", entry(Posting{a, Debit, egp(t, 1)}, Posting{b, Debit, egp(t, 1)}), ErrUnbalanced},
		{"zero amount", entry(Posting{a, Debit, egp(t, 0)}, Posting{b, Credit, egp(t, 0)}), ErrNonPositiveAmount},
		{"negative amount", entry(Posting{a, Debit, egp(t, -5)}, Posting{b, Credit, egp(t, -5)}), ErrNonPositiveAmount},
		{"zero-value money", entry(Posting{a, Debit, money.Money{}}, Posting{b, Credit, egp(t, 1)}), money.ErrUnsupportedCurrency},
		{"nil transaction", Entry{Type: EntryP2P, Postings: []Posting{{a, Debit, egp(t, 1)}, {b, Credit, egp(t, 1)}}}, ErrMissingTransactionID},
		{"mixed currency", entry(Posting{a, Debit, egp(t, 1)}, Posting{b, Credit, money.Money{}}), ErrCurrencyMismatch},
		{"bad direction", entry(Posting{a, "SIDEWAYS", egp(t, 1)}, Posting{b, Credit, egp(t, 1)}), ErrInvalidDirection},
		{"overflowing sum", entry(Posting{a, Debit, egp(t, math.MaxInt64)}, Posting{a, Debit, egp(t, 1)}, Posting{b, Credit, egp(t, 1)}), money.ErrOverflow},
		{"unknown type", Entry{TransactionID: uuid.New(), Type: "GIFT", Postings: []Posting{{a, Debit, egp(t, 1)}, {b, Credit, egp(t, 1)}}}, ErrUnknownEntryType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.validate()
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestIntegrityReport_OK(t *testing.T) {
	assert.True(t, IntegrityReport{TotalDebits: 5, TotalCredits: 5}.OK())
	assert.False(t, IntegrityReport{TotalDebits: 5, TotalCredits: 4}.OK())
	assert.False(t, IntegrityReport{Drifts: []AccountDrift{{}}}.OK())
	assert.False(t, IntegrityReport{UnbalancedEntries: []UnbalancedEntry{{}}}.OK())
}

func TestCompareIDs_ByteOrder(t *testing.T) {
	low := AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000001"))
	high := AccountID(uuid.MustParse("ffffffff-0000-7000-8000-000000000000"))

	assert.Negative(t, compareIDs(low, high))
	assert.Positive(t, compareIDs(high, low))
	assert.Zero(t, compareIDs(low, low))
}

func TestCompareLockOrder_UserWalletsBeforeSystem(t *testing.T) {
	bank := AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000001"))
	inTransit := AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000002"))
	userLow := AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000009"))
	userHigh := AccountID(uuid.MustParse("ffffffff-0000-7000-8000-000000000000"))

	ids := []AccountID{inTransit, userHigh, bank, userLow}
	slices.SortStableFunc(ids, compareLockOrder)

	assert.Equal(t, []AccountID{userLow, userHigh, bank, inTransit}, ids)
}

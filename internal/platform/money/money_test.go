package money

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustNew(t *testing.T, amount int64) Money {
	t.Helper()
	m, err := New(amount, EGP)
	require.NoError(t, err)
	return m
}

func TestNew_Sign(t *testing.T) {
	for _, amount := range []int64{-500, 0, 1, math.MaxInt64, math.MinInt64} {
		m, err := New(amount, EGP)
		require.NoError(t, err, "amount %d", amount)
		assert.Equal(t, amount, m.Amount())
		assert.Equal(t, EGP, m.Currency())
	}
	m := mustNew(t, -500)
	assert.True(t, m.IsNegative())
	assert.False(t, m.IsZero())
	assert.True(t, mustNew(t, 0).IsZero())
}

func TestNewPositive(t *testing.T) {
	tests := []struct {
		amount  int64
		wantErr error
	}{
		{0, ErrNotPositive},
		{-1, ErrNotPositive},
		{math.MinInt64, ErrNotPositive},
		{1, nil},
		{15075, nil},
		{math.MaxInt64, nil},
	}
	for _, tt := range tests {
		m, err := NewPositive(tt.amount, EGP)
		if tt.wantErr != nil {
			require.ErrorIs(t, err, tt.wantErr, "amount %d", tt.amount)
			assert.Equal(t, Money{}, m)
			continue
		}
		require.NoError(t, err, "amount %d", tt.amount)
		assert.Equal(t, tt.amount, m.Amount())
	}

	_, err := NewPositive(100, "USD")
	assert.ErrorIs(t, err, ErrUnsupportedCurrency)
}

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		code    string
		wantErr bool
	}{
		{"EGP", false},
		{"USD", true},
		{"", true},
		{"egp", true},
		{" EGP", true},
	}
	for _, tt := range tests {
		c, err := ParseCurrency(tt.code)
		if tt.wantErr {
			require.ErrorIs(t, err, ErrUnsupportedCurrency, "code %q", tt.code)
			_, err = New(1, Currency(tt.code))
			assert.ErrorIs(t, err, ErrUnsupportedCurrency, "New with code %q", tt.code)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, EGP, c)
	}
}

func TestAdd(t *testing.T) {
	sum, err := mustNew(t, 15000).Add(mustNew(t, 75))
	require.NoError(t, err)
	assert.Equal(t, int64(15075), sum.Amount())
	assert.Equal(t, EGP, sum.Currency())

	sum, err = mustNew(t, -100).Add(mustNew(t, 40))
	require.NoError(t, err)
	assert.Equal(t, int64(-60), sum.Amount())
}

func TestSub(t *testing.T) {
	diff, err := mustNew(t, 100).Sub(mustNew(t, 250))
	require.NoError(t, err)
	assert.Equal(t, int64(-150), diff.Amount())
}

func TestAdd_Overflow(t *testing.T) {
	tests := []struct{ a, b int64 }{
		{math.MaxInt64, 1},
		{math.MaxInt64 - 5, 6},
		{math.MinInt64, -1},
		{-1, math.MinInt64},
	}
	for _, tt := range tests {
		got, err := mustNew(t, tt.a).Add(mustNew(t, tt.b))
		require.ErrorIs(t, err, ErrOverflow, "%d + %d", tt.a, tt.b)
		assert.Equal(t, Money{}, got)
	}
	got, err := mustNew(t, math.MaxInt64).Add(mustNew(t, math.MinInt64))
	require.NoError(t, err)
	assert.Equal(t, int64(-1), got.Amount())
}

func TestSub_Overflow(t *testing.T) {
	tests := []struct{ a, b int64 }{
		{math.MinInt64, 1},
		{math.MaxInt64, -1},
		{0, math.MinInt64},
		{-2, math.MaxInt64},
	}
	for _, tt := range tests {
		got, err := mustNew(t, tt.a).Sub(mustNew(t, tt.b))
		require.ErrorIs(t, err, ErrOverflow, "%d - %d", tt.a, tt.b)
		assert.Equal(t, Money{}, got)
	}
	got, err := mustNew(t, -1).Sub(mustNew(t, math.MaxInt64))
	require.NoError(t, err)
	assert.Equal(t, int64(math.MinInt64), got.Amount())
}

func TestAddSub_CurrencyMismatch(t *testing.T) {
	egp := mustNew(t, 100)
	var zero Money

	_, err := egp.Add(zero)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	_, err = zero.Add(egp)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	_, err = zero.Add(zero)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	_, err = egp.Sub(zero)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	_, err = egp.Sub(Money{amount: 1, currency: "USD"})
	require.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestFormat(t *testing.T) {
	tests := []struct {
		amount int64
		want   string
	}{
		{15075, "150.75 EGP"},
		{5, "0.05 EGP"},
		{0, "0.00 EGP"},
		{100, "1.00 EGP"},
		{10, "0.10 EGP"},
		{-5, "-0.05 EGP"},
		{-15075, "-150.75 EGP"},
		{math.MaxInt64, "92233720368547758.07 EGP"},
		{math.MinInt64, "-92233720368547758.08 EGP"},
	}
	for _, tt := range tests {
		m := mustNew(t, tt.amount)
		assert.Equal(t, tt.want, m.Format(), "amount %d", tt.amount)
		assert.Equal(t, tt.want, m.String())
	}
}

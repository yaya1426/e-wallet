package transaction

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/money"
)

func egp(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.NewPositive(amount, money.EGP)
	require.NoError(t, err)
	return m
}

func TestNewTransaction_Validate(t *testing.T) {
	user, a, b := uuid.New(), uuid.New(), uuid.New()
	valid := map[Type]New{
		TypeTopUp:      {Type: TypeTopUp, Status: StatusPending, InitiatorUserID: user, DestinationWalletID: a, Amount: egp(t, 100), IdempotencyKey: "k"},
		TypeWithdrawal: {Type: TypeWithdrawal, Status: StatusPending, InitiatorUserID: user, SourceWalletID: a, Amount: egp(t, 100), IdempotencyKey: "k"},
		TypeP2P:        {Type: TypeP2P, Status: StatusCompleted, InitiatorUserID: user, SourceWalletID: a, DestinationWalletID: b, Amount: egp(t, 100), IdempotencyKey: "k"},
	}
	for typ, n := range valid {
		assert.NoError(t, n.validate(), typ)
	}

	tests := []struct {
		name   string
		change func(n *New)
		base   Type
		want   error
	}{
		{"P2P created PENDING", func(n *New) { n.Status = StatusPending }, TypeP2P, ErrInvalidInitialStatus},
		{"top-up created COMPLETED", func(n *New) { n.Status = StatusCompleted }, TypeTopUp, ErrInvalidInitialStatus},
		{"withdrawal created PROCESSING", func(n *New) { n.Status = StatusProcessing }, TypeWithdrawal, ErrInvalidInitialStatus},
		{"top-up with a source wallet", func(n *New) { n.SourceWalletID = b }, TypeTopUp, ErrInvalidWallets},
		{"top-up without destination", func(n *New) { n.DestinationWalletID = uuid.Nil }, TypeTopUp, ErrInvalidWallets},
		{"withdrawal with a destination", func(n *New) { n.DestinationWalletID = b }, TypeWithdrawal, ErrInvalidWallets},
		{"P2P to the same wallet", func(n *New) { n.DestinationWalletID = a }, TypeP2P, ErrInvalidWallets},
		{"P2P without source", func(n *New) { n.SourceWalletID = uuid.Nil }, TypeP2P, ErrInvalidWallets},
		{"unknown type", func(n *New) { n.Type = "GIFT" }, TypeP2P, ErrInvalidTransaction},
		{"no initiator", func(n *New) { n.InitiatorUserID = uuid.Nil }, TypeTopUp, ErrInvalidTransaction},
		{"zero amount", func(n *New) { n.Amount = money.Money{} }, TypeTopUp, ErrInvalidTransaction},
		{"no idempotency key", func(n *New) { n.IdempotencyKey = "" }, TypeTopUp, ErrInvalidTransaction},
		{"141-character note", func(n *New) { n.Note = strings.Repeat("ن", 141) }, TypeP2P, ErrInvalidTransaction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := valid[tt.base]
			tt.change(&n)
			assert.ErrorIs(t, n.validate(), tt.want)
		})
	}

	n := valid[TypeP2P]
	n.Note = strings.Repeat("ن", 140)
	assert.NoError(t, n.validate(), "140 characters is the limit")
}

func TestTransitionTable(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusPending, StatusProcessing}:   true,
		{StatusPending, StatusFailed}:       true,
		{StatusProcessing, StatusCompleted}: true,
		{StatusProcessing, StatusFailed}:    true,
	}
	all := []Status{StatusPending, StatusProcessing, StatusCompleted, StatusFailed}
	for _, from := range all {
		for _, to := range all {
			assert.Equal(t, allowed[[2]Status{from, to}], CanTransition(from, to), "%s → %s", from, to)
		}
	}
	assert.True(t, StatusCompleted.Terminal())
	assert.True(t, StatusFailed.Terminal())
	assert.False(t, StatusPending.Terminal())
	assert.False(t, StatusProcessing.Terminal())
}

func TestStatusConflictError(t *testing.T) {
	var err error = &StatusConflictError{Expected: StatusPending, Current: StatusProcessing}
	assert.ErrorIs(t, err, ErrStatusConflict)
	var conflict *StatusConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, StatusProcessing, conflict.Current)
}

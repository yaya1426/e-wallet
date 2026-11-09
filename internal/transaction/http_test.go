package transaction

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLimit(t *testing.T) {
	n, err := parseLimit("")
	require.NoError(t, err)
	assert.Equal(t, 20, n)
	for _, ok := range []string{"1", "100", "37"} {
		_, err := parseLimit(ok)
		assert.NoError(t, err, ok)
	}
	for _, bad := range []string{"0", "101", "abc", "-1", "2.5"} {
		_, err := parseLimit(bad)
		assert.ErrorIs(t, err, ErrInvalidLimit, bad)
	}
}

func TestDecodeCursor(t *testing.T) {
	c := Cursor{CreatedAt: time.Date(2026, 11, 9, 10, 0, 0, 123456000, time.UTC), ID: uuid.New()}
	got, err := DecodeCursor(c.Encode())
	require.NoError(t, err)
	assert.True(t, c.CreatedAt.Equal(got.CreatedAt))
	assert.Equal(t, c.ID, got.ID)

	for _, bad := range []string{
		"garbage!!",
		base64.RawURLEncoding.EncodeToString([]byte("not json")),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":"yesterday","id":"` + uuid.NewString() + `"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":"2026-11-09T10:00:00Z","id":"nope"}`)),
	} {
		_, err := DecodeCursor(bad)
		assert.ErrorIs(t, err, ErrInvalidCursor, bad)
	}
}

type countingParties struct {
	calls int
	seen  []uuid.UUID
}

func (p *countingParties) ByWallet(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]Party, error) {
	p.calls++
	p.seen = append(p.seen, ids...)
	out := map[uuid.UUID]Party{}
	for _, id := range ids {
		out[id] = Party{Phone: "+20100000" + id.String()[:4], MaskedName: "X."}
	}
	return out, nil
}

func TestList_BatchesParties(t *testing.T) {
	parties := &countingParties{}
	h := NewHandler(nil, nil, nil, parties, slog.New(slog.NewTextHandler(io.Discard, nil)))
	me, a, b := uuid.New(), uuid.New(), uuid.New()
	txns := []Transaction{
		{Type: TypeP2P, SourceWalletID: me, DestinationWalletID: a},
		{Type: TypeP2P, SourceWalletID: b, DestinationWalletID: me},
		{Type: TypeTopUp, DestinationWalletID: me},
		{Type: TypeP2P, SourceWalletID: me, DestinationWalletID: b},
	}

	items, err := h.items(httptest.NewRequest("GET", "/", nil), me, txns)
	require.NoError(t, err)

	assert.Equal(t, 1, parties.calls, "one Parties call per page")
	assert.ElementsMatch(t, []uuid.UUID{a, b, b}, parties.seen, "the other side of each P2P")
	assert.Equal(t, DirectionOut, items[0].Direction)
	assert.Equal(t, DirectionIn, items[1].Direction)
	assert.Nil(t, items[2].Counterparty, "no counterparty for a top-up")
	assert.NotNil(t, items[0].Counterparty)

	parties.calls = 0
	_, err = h.items(httptest.NewRequest("GET", "/", nil), me, []Transaction{{Type: TypeTopUp, DestinationWalletID: me}})
	require.NoError(t, err)
	assert.Zero(t, parties.calls, "no call when the page has no P2P")
}

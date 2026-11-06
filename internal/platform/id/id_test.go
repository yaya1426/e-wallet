package id

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Version7(t *testing.T) {
	u := New()

	assert.Equal(t, uuid.Version(7), u.Version())
	assert.Equal(t, uuid.RFC4122, u.Variant())
}

func TestNew_UniqueAndOrdered(t *testing.T) {
	const n = 1000
	seen := make(map[uuid.UUID]struct{}, n)
	prev := New()
	seen[prev] = struct{}{}
	for i := 1; i < n; i++ {
		u := New()
		_, dup := seen[u]
		require.False(t, dup, "duplicate id %s", u)
		seen[u] = struct{}{}
		assert.LessOrEqual(t, bytes.Compare(prev[:], u[:]), 0, "ids must not go back in time: %s then %s", prev, u)
		prev = u
	}
}

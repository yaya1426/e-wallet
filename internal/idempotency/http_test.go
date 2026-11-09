package idempotency

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyFromRequest(t *testing.T) {
	tests := []struct {
		name, header string
		want         error
	}{
		{"missing", "", ErrKeyRequired},
		{"v4", "8f0f4c8e-6a3b-4d1e-9c2a-0b1e2d3c4f5a", nil},
		{"v7", "01a1c09e-f2a9-757a-a7e1-38aeb3f8e0e9", nil},
		{"v1", "c232ab00-9414-11ec-b3c8-9f6bdeced846", nil},
		{"not a uuid", "abc", ErrKeyInvalid},
		{"65 characters", strings.Repeat("a", 65), ErrKeyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.header != "" {
				req.Header.Set(HeaderKey, tt.header)
			}
			key, err := KeyFromRequest(req)
			if tt.want != nil {
				assert.ErrorIs(t, err, tt.want)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.header, key)
		})
	}
}

type sampleRequest struct {
	Phone  string `json:"phone"`
	Amount int64  `json:"amount_minor"`
	Note   string `json:"note"`
}

func TestRequestHash_Canonical(t *testing.T) {
	decode := func(body string) sampleRequest {
		var r sampleRequest
		require.NoError(t, json.Unmarshal([]byte(body), &r))
		return r
	}
	hash := func(v any) []byte {
		h, err := RequestHash(v)
		require.NoError(t, err)
		return h
	}
	a := decode(`{"phone":"+201001234567","amount_minor":500,"note":"x"}`)
	b := decode("{ \"note\" : \"x\",\n  \"amount_minor\": 500, \"phone\":\"+201001234567\" }")

	assert.Equal(t, hash(a), hash(b), "key order and whitespace do not matter")
	assert.Len(t, hash(a), 32)

	for _, changed := range []sampleRequest{
		{Phone: "+201001234568", Amount: 500, Note: "x"},
		{Phone: "+201001234567", Amount: 501, Note: "x"},
		{Phone: "+201001234567", Amount: 500, Note: "y"},
	} {
		assert.NotEqual(t, hash(a), hash(changed), "%+v", changed)
	}
}

func TestWriteReplay(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteReplay(rec, Replay{Status: http.StatusCreated, Body: json.RawMessage(`{"ok":true}`)})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "true", rec.Header().Get(HeaderReplayed))
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{ErrKeyRequired, 400, CodeKeyRequired},
		{ErrKeyInvalid, 400, CodeKeyInvalid},
		{ErrKeyReused, 422, CodeKeyReused},
		{ErrRequestInProgress, 409, CodeRequestInProgress},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		assert.True(t, WriteError(rec, tt.err))
		assert.Equal(t, tt.status, rec.Code)
		assert.Contains(t, rec.Body.String(), `"code":"`+tt.code+`"`)
	}
	assert.False(t, WriteError(httptest.NewRecorder(), errors.New("other")))
}

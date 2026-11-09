//go:build integration

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPreviewRecipient(t *testing.T) {
	f := newAPIFixture(t)
	a := f.newParty(t, "+201001234567")
	f.newParty(t, "+201001234568") // registered as "Ahmed Mohamed Ali"

	tests := []struct {
		name, token, query string
		status             int
		code               string
	}{
		{"registered", a.token, "?phone=%2B201001234568", 200, ""},
		{"unknown", a.token, "?phone=%2B201509999999", 404, "RECIPIENT_NOT_FOUND"},
		{"own phone", a.token, "?phone=%2B201001234567", 422, "SELF_TRANSFER_NOT_ALLOWED"},
		{"local format", a.token, "?phone=01001234568", 400, "INVALID_PHONE"},
		{"unescaped plus becomes a space", a.token, "?phone=+201001234568", 400, "INVALID_PHONE"},
		{"missing", a.token, "", 400, "INVALID_PHONE"},
		{"no token", "", "?phone=%2B201001234568", 401, "UNAUTHENTICATED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.call(t, http.MethodGet, "/v1/recipients"+tt.query, tt.token, "")
			assert.Equal(t, tt.status, r.status, r.body)
			if tt.code != "" {
				assert.Equal(t, tt.code, errorCode(r))
				return
			}
			assert.Equal(t, map[string]any{"phone": "+201001234568", "masked_name": "Ahmed M. A."}, r.body)
		})
	}
}

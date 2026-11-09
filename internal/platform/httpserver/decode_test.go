package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type decodeTarget struct {
	Phone string `json:"phone"`
	Count int64  `json:"count"`
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int // 0 means success
		wantCode   string
	}{
		{"valid object", `{"phone":"+201001234567","count":3}`, 0, ""},
		{"valid with surrounding space", " \n{\"phone\":\"x\"}\n ", 0, ""},
		{"unknown field", `{"phone":"x","extra":1}`, http.StatusBadRequest, CodeMalformedRequest},
		{"array body", `[{"phone":"x"}]`, http.StatusBadRequest, CodeMalformedRequest},
		{"trailing data", `{"phone":"x"} {"phone":"y"}`, http.StatusBadRequest, CodeMalformedRequest},
		{"trailing garbage", `{"phone":"x"}garbage`, http.StatusBadRequest, CodeMalformedRequest},
		{"wrong type: number for string", `{"phone":123}`, http.StatusBadRequest, CodeMalformedRequest},
		{"wrong type: decimal for int64", `{"count":1.5}`, http.StatusBadRequest, CodeMalformedRequest},
		{"wrong type: string for int64", `{"count":"3"}`, http.StatusBadRequest, CodeMalformedRequest},
		{"empty body", ``, http.StatusBadRequest, CodeMalformedRequest},
		{"null body", `null`, http.StatusBadRequest, CodeMalformedRequest},
		{"string body", `"hello"`, http.StatusBadRequest, CodeMalformedRequest},
		{"number body", `42`, http.StatusBadRequest, CodeMalformedRequest},
		{"whitespace only", " \n ", http.StatusBadRequest, CodeMalformedRequest},
		{"invalid JSON", `{"phone":`, http.StatusBadRequest, CodeMalformedRequest},
		{"65 KB body", `{"phone":"` + strings.Repeat("a", 65<<10) + `"}`, http.StatusRequestEntityTooLarge, CodeRequestTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			var dst decodeTarget

			err := DecodeJSON(rec, req, &dst)

			if tt.wantStatus == 0 {
				require.NoError(t, err)
				return
			}
			var de *DecodeError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, tt.wantStatus, de.Status)
			assert.Equal(t, tt.wantCode, de.Code)

			WriteDecodeError(rec, err)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), `"code":"`+tt.wantCode+`"`)
		})
	}
}

func TestDecodeJSON_ExactlyAtLimitIsAccepted(t *testing.T) {
	prefix, suffix := `{"phone":"`, `"}`
	body := prefix + strings.Repeat("a", MaxBodyBytes-len(prefix)-len(suffix)) + suffix
	require.Len(t, body, MaxBodyBytes)

	var dst decodeTarget
	err := DecodeJSON(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &dst)
	assert.NoError(t, err)
}

func TestUserID_RoundTrip(t *testing.T) {
	_, ok := UserID(context.Background())
	assert.False(t, ok)

	want := uuid.New()
	got, ok := UserID(WithUserID(context.Background(), want))
	assert.True(t, ok)
	assert.Equal(t, want, got)
}

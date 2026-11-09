package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/database"
)

func TestWriteTryAgain(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set(HeaderRequestID, "req-1")
	// InTx returns ErrTryAgain wrapped together with the last *pgconn.PgError.
	exhausted := fmt.Errorf("%w: %w", database.ErrTryAgain, &pgconn.PgError{Code: "40P01"})

	require.True(t, WriteTryAgain(rec, exhausted))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body errorBody
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, CodeTryAgain, body.Error.Code)
	assert.Equal(t, "req-1", body.Error.RequestID)
	assert.NotEmpty(t, body.Error.Message)

	assert.False(t, WriteTryAgain(httptest.NewRecorder(), errors.New("other")))
}

func TestErrorBody_EqualsWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set(HeaderRequestID, "req-2")

	stored, err := json.Marshal(ErrorBody(rec, "SOME_CODE", "Some message."))
	require.NoError(t, err)
	WriteError(rec, http.StatusUnprocessableEntity, "SOME_CODE", "Some message.")

	assert.JSONEq(t, string(stored), rec.Body.String())
}

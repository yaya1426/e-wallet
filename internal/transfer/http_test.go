package transfer

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/wallet"
)

func TestValidate_Order(t *testing.T) {
	user := uuid.New()
	valid := sendRequest{RecipientPhone: "+201001234567", AmountMinor: 500, Currency: "EGP", Note: " hi "}
	in, err := validate(user, valid)
	require.NoError(t, err)
	assert.Equal(t, "hi", in.Note)
	assert.Equal(t, int64(500), in.Amount.Amount())

	allBad := sendRequest{RecipientPhone: "bad", AmountMinor: 0, Currency: "USD", Note: strings.Repeat("n", 141)}
	steps := []struct {
		fix  func(*sendRequest)
		want error
	}{
		{func(*sendRequest) {}, ErrInvalidAmount},                                     // 4 before 5, 6, 7
		{func(r *sendRequest) { r.AmountMinor = 500 }, ErrUnsupportedCurrency},        // 5 before 6, 7
		{func(r *sendRequest) { r.Currency = "EGP" }, ErrInvalidPhone},                // 6 before 7
		{func(r *sendRequest) { r.RecipientPhone = "+201001234567" }, ErrInvalidNote}, // 7
	}
	req := allBad
	for _, step := range steps {
		step.fix(&req)
		_, err := validate(user, req)
		assert.ErrorIs(t, err, step.want)
	}
	_, err = validate(user, sendRequest{RecipientPhone: "+201001234567", AmountMinor: -1, Currency: "EGP"})
	assert.ErrorIs(t, err, ErrInvalidAmount)
}

func TestNormalizeNote(t *testing.T) {
	note, err := NormalizeNote("  " + strings.Repeat("ن", 140) + "  ")
	require.NoError(t, err, "140 Arabic characters (280 bytes) fit")
	assert.Equal(t, strings.Repeat("ن", 140), note)
	_, err = NormalizeNote(strings.Repeat("ن", 141))
	assert.ErrorIs(t, err, ErrInvalidNote)
	note, err = NormalizeNote("   ")
	require.NoError(t, err)
	assert.Empty(t, note)
}

func TestRender_BusinessErrorTable(t *testing.T) {
	for _, be := range businessErrors {
		wrapped := errors.Join(errors.New("context"), be.err)
		assert.True(t, isBusinessError(wrapped), be.code)
		rec := httptest.NewRecorder()
		status, body := render(rec, Result{}, wrapped)
		assert.Equal(t, be.status, status, be.code)
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"code":"`+be.code+`"`)
	}

	unknown := errors.New("not a business error")
	assert.False(t, isBusinessError(unknown))
	status, body := render(httptest.NewRecorder(), Result{}, unknown)
	assert.Equal(t, http.StatusInternalServerError, status, "fails safe instead of picking a business code")
	encoded, _ := json.Marshal(body)
	assert.Contains(t, string(encoded), "INTERNAL_ERROR")
	assert.False(t, isBusinessError(wallet.ErrWalletLimitExceeded), "top-up error is not a P2P business error")
}

func TestWriteValidationError_Unknown(t *testing.T) {
	rec := httptest.NewRecorder()
	writeValidationError(rec, errors.New("unexpected"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

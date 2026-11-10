// Package openapi_test checks api/openapi/wallet.yaml: it is a valid OpenAPI 3.1 document
// (T004 AC-10) and it catalogues every error code the handlers can return (TDR-017 §3).
package openapi_test

import (
	"os"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi-validator/schema_validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/payment"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/transfer"
	"github.com/yaya1426/e-wallet/internal/wallet"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

const specFile = "wallet.yaml"

func loadSpec(t *testing.T) ([]byte, libopenapi.Document) {
	t.Helper()
	raw, err := os.ReadFile(specFile)
	require.NoError(t, err)
	doc, err := libopenapi.NewDocument(raw)
	require.NoError(t, err)
	return raw, doc
}

func TestSpec_IsValidOpenAPI31(t *testing.T) {
	_, doc := loadSpec(t)

	assert.Equal(t, "3.1.0", doc.GetVersion())
	valid, errs := schema_validation.ValidateOpenAPIDocument(doc)
	for _, e := range errs {
		t.Errorf("%s: %s", e.Message, e.Reason)
	}
	assert.True(t, valid)
}

func TestSpec_DescribesTheEndpoints(t *testing.T) {
	_, doc := loadSpec(t)
	model, err := doc.BuildV3Model()
	require.NoError(t, err)
	paths := model.Model.Paths.PathItems

	tests := []struct{ path, method string }{
		{"/v1/auth/register", "post"},
		{"/v1/auth/login", "post"},
		{"/v1/auth/refresh", "post"},
		{"/v1/auth/logout", "post"},
		{"/v1/me/wallet", "get"},
		{"/v1/me/bank-account", "put"},
		{"/v1/me/bank-account", "get"},
		{"/v1/transfers", "post"},
		{"/v1/recipients", "get"},
		{"/v1/me/transactions", "get"},
		{"/v1/transactions/{id}", "get"},
		{"/v1/topups", "post"},
		{"/v1/withdrawals", "post"},
		{"/webhooks/bank", "post"},
		{"/internal/v1/discrepancies", "get"},
		{"/internal/v1/discrepancies/{id}/resolve", "post"},
		{"/internal/v1/wallets/{wallet_id}/freeze", "post"},
		{"/internal/v1/wallets/{wallet_id}/unfreeze", "post"},
	}
	for _, tt := range tests {
		item, ok := paths.Get(tt.path)
		require.True(t, ok, "path %s", tt.path)
		assert.NotNil(t, item.GetOperations().GetOrZero(tt.method), "%s %s", tt.method, tt.path)
	}
	_, ok := model.Model.Components.Schemas.Get("Error")
	assert.True(t, ok, "shared error schema")
}

func TestSpec_CataloguesEveryErrorCode(t *testing.T) {
	raw, _ := loadSpec(t)
	codes := []string{
		httpserver.CodeMalformedRequest, httpserver.CodeRequestTooLarge, httpserver.CodeTryAgain, httpserver.CodeInternal,
		identity.CodeInvalidPhone, identity.CodeInvalidPassword, identity.CodeInvalidName,
		identity.CodePhoneAlreadyRegistered, identity.CodeInvalidCredentials,
		identity.CodeUserDisabled, identity.CodeTooManyRequests, identity.CodeUnauthenticated, identity.CodeInvalidRefreshToken,
		identity.CodeInvalidIBAN, identity.CodeInvalidHolderName, identity.CodeBankAccountNotFound,
		idempotency.CodeKeyRequired, idempotency.CodeKeyInvalid, idempotency.CodeKeyReused, idempotency.CodeRequestInProgress,
		wallet.CodeInvalidAmount, wallet.CodeUnsupportedCurrency, wallet.CodeAmountOutOfRange, wallet.CodeWalletFrozen,
		wallet.CodeInsufficientFunds, wallet.CodeDailyLimitExceeded, wallet.CodeRecipientLimitExceeded,
		transfer.CodeInvalidNote, identity.CodeRecipientNotFound, identity.CodeSelfTransferNotAllowed,
		transaction.CodeInvalidLimit, transaction.CodeInvalidCursor, transaction.CodeTransactionNotFound,
		webhook.CodeInvalidSignature,
		discrepancy.CodeInvalidFilter, discrepancy.CodeDiscrepancyNotFound, discrepancy.CodeAlreadyResolved, discrepancy.CodeInvalidNote,
		wallet.CodeWalletNotFound,
		payment.CodeBankAccountRequired, wallet.CodeWalletLimitExceeded,
	}
	for _, code := range codes {
		assert.True(t, strings.Contains(string(raw), "`"+code+"`"), "error code %s is missing from %s", code, specFile)
	}
}

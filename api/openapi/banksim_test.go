package openapi_test

import (
	"os"
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi-validator/schema_validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBanksimSpec_IsValidOpenAPI31 lints the bank simulator contract like wallet.yaml (T013 AC-7).
func TestBanksimSpec_IsValidOpenAPI31(t *testing.T) {
	raw, err := os.ReadFile("banksim.yaml")
	require.NoError(t, err)
	doc, err := libopenapi.NewDocument(raw)
	require.NoError(t, err)

	assert.Equal(t, "3.1.0", doc.GetVersion())
	valid, errs := schema_validation.ValidateOpenAPIDocument(doc)
	for _, e := range errs {
		t.Errorf("%s: %s", e.Message, e.Reason)
	}
	assert.True(t, valid)

	model, err := doc.BuildV3Model()
	require.NoError(t, err)
	for path, method := range map[string]string{
		"/v1/debits": "post", "/v1/payouts": "post", "/v1/operations/{idempotency_key}": "get",
	} {
		item, ok := model.Model.Paths.PathItems.Get(path)
		require.True(t, ok, "path %s", path)
		assert.NotNil(t, item.GetOperations().GetOrZero(method), "%s %s", method, path)
	}
}

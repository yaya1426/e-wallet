package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskName(t *testing.T) {
	tests := map[string]string{
		"Ahmed Mohamed Ali": "Ahmed M. A.",
		"Mona":              "Mona",
		"  sara   ali ":     "sara a.",
		"أحمد محمد":         "أحمد م.",
		"Ahmed\tMohamed":    "Ahmed M.",
		"":                  "",
	}
	for in, want := range tests {
		assert.Equal(t, want, MaskName(in), "%q", in)
	}
}

func TestValidPhone(t *testing.T) {
	assert.True(t, ValidPhone("+201001234567"))
	assert.False(t, ValidPhone("01001234567"))
}

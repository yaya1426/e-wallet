package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeIBAN(t *testing.T) {
	tests := []struct {
		name, in, want string // want "" means ErrInvalidIBAN
	}{
		{"banksim scenario 0000", "EG890003000100000000123450000", "EG890003000100000000123450000"},
		{"banksim scenario 0001", "EG620003000100000000123450001", "EG620003000100000000123450001"},
		{"banksim scenario 0002", "EG350003000100000000123450002", "EG350003000100000000123450002"},
		{"spaces and lower case", "eg89 0003 0001 0000 0000 1234 5000 0", "EG890003000100000000123450000"},
		{"wrong checksum", "EG880003000100000000123450000", ""},
		{"one digit changed", "EG890003000100000000123450009", ""},
		{"wrong country (valid UK IBAN)", "GB82WEST12345698765432", ""},
		{"EG prefix, too short", "EG89000300010000000012345000", ""},
		{"EG prefix, too long", "EG8900030001000000001234500000", ""},
		{"letters in the BBAN", "EG8900030001000000001234500A0", ""},
		{"tab instead of space", "EG89\t0003000100000000123450000", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeIBAN(tt.in)
			if tt.want == "" {
				assert.ErrorIs(t, err, ErrInvalidIBAN)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMod97_KnownValues(t *testing.T) {
	// ISO 13616 example: GB82WEST12345698765432 rearranged has remainder 1.
	assert.Equal(t, 1, mod97("WEST12345698765432GB82"))
	assert.Equal(t, 0, mod97("97"))
	assert.Equal(t, 10, mod97("A"))
}

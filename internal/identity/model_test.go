package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validInput() RegisterInput {
	return RegisterInput{Phone: "+201001234567", FullName: "Ahmed Mohamed Ali", Password: "s3cret-pass"}
}

func TestValidatePhone(t *testing.T) {
	tests := []struct {
		phone string
		ok    bool
	}{
		{"+201001234567", true}, {"+201101234567", true}, {"+201201234567", true}, {"+201501234567", true},
		{"+201301234567", false}, {"+201401234567", false}, // no such mobile prefixes
		{"01001234567", false},    // local format is not accepted
		{"+20100123456", false},   // too short
		{"+2010012345678", false}, // too long
		{"+20 1001234567", false}, {"+20100123456a", false}, {"", false},
		{"+441001234567", false}, // not Egypt
	}
	for _, tt := range tests {
		in := validInput()
		in.Phone = tt.phone
		_, err := in.validate()
		if tt.ok {
			assert.NoError(t, err, tt.phone)
		} else {
			assert.ErrorIs(t, err, ErrInvalidPhone, tt.phone)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		ok       bool
	}{
		{"7 bytes", strings.Repeat("p", 7), false},
		{"8 bytes", strings.Repeat("p", 8), true},
		{"72 bytes", strings.Repeat("p", 72), true},
		{"73 bytes", strings.Repeat("p", 73), false},
		{"multi-byte counts bytes: 4 runes = 8 bytes", "ممكن", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		in := validInput()
		in.Password = tt.password
		_, err := in.validate()
		if tt.ok {
			assert.NoError(t, err, tt.name)
		} else {
			assert.ErrorIs(t, err, ErrInvalidPassword, tt.name)
		}
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name, fullName, want string
		ok                   bool
	}{
		{"trimmed", "  Ahmed Ali  ", "Ahmed Ali", true},
		{"two runes", "Al", "Al", true},
		{"arabic, counted in runes", "أحمد", "أحمد", true},
		{"100 runes", strings.Repeat("ب", 100), strings.Repeat("ب", 100), true},
		{"one rune", "A", "", false},
		{"only spaces", "    ", "", false},
		{"101 runes", strings.Repeat("ب", 101), "", false},
	}
	for _, tt := range tests {
		in := validInput()
		in.FullName = tt.fullName
		out, err := in.validate()
		if tt.ok {
			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.want, out.FullName, tt.name)
		} else {
			assert.ErrorIs(t, err, ErrInvalidName, tt.name)
		}
	}
}

func TestValidate_OrderPhonePasswordName(t *testing.T) {
	_, err := RegisterInput{Phone: "bad", Password: "x", FullName: ""}.validate()
	assert.ErrorIs(t, err, ErrInvalidPhone)
	_, err = RegisterInput{Phone: "+201001234567", Password: "x", FullName: ""}.validate()
	assert.ErrorIs(t, err, ErrInvalidPassword)
}

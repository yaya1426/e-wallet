package identity

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalidIBAN means the IBAN is not a valid Egyptian IBAN (T005).
var ErrInvalidIBAN = errors.New("identity: invalid IBAN")

// egyptianIBAN is a normalized Egyptian IBAN: EG, 2 check digits, then a 25-digit BBAN.
var egyptianIBAN = regexp.MustCompile(`^EG[0-9]{27}$`)

// NormalizeIBAN removes spaces, upper-cases the letters and checks that the result is an
// Egyptian IBAN with a valid ISO 13616 mod-97 check (remainder 1). It returns the
// 29-character normalized form.
func NormalizeIBAN(s string) (string, error) {
	iban := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if !egyptianIBAN.MatchString(iban) || mod97(iban[4:]+iban[:4]) != 1 {
		return "", ErrInvalidIBAN
	}
	return iban, nil
}

// mod97 returns the ISO 13616 remainder of s, where letters count as two-digit numbers
// (A=10 … Z=35). It works digit by digit on a small integer, so the 30+ digit number is
// never built (no big or floating-point numbers). s must contain only 0-9 and A-Z.
func mod97(s string) int {
	remainder := 0
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			remainder = (remainder*10 + int(c-'0')) % 97
		default: // 'A'..'Z' → 10..35, two digits
			remainder = (remainder*100 + int(c-'A') + 10) % 97
		}
	}
	return remainder
}

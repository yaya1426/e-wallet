package identity

import (
	"strings"
	"unicode/utf8"
)

// MaskName shows a full name to another user without revealing it fully (PRD-004 P2P-BR-5,
// T008): the first word in full, then the first letter of every other word followed by ".".
// Words are split on Unicode white space, e.g. "Ahmed Mohamed Ali" → "Ahmed M. A.".
func MaskName(fullName string) string {
	words := strings.Fields(fullName)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(words[0])
	for _, w := range words[1:] {
		first, _ := utf8.DecodeRuneInString(w)
		b.WriteByte(' ')
		b.WriteRune(first)
		b.WriteByte('.')
	}
	return b.String()
}

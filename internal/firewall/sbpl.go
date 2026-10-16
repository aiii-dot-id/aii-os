package firewall

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func SBPLString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%q is not valid UTF-8, so it is not written into a Seatbelt profile", s)
	}
	if strings.IndexByte(s, 0) >= 0 {
		return "", fmt.Errorf("%q holds NUL, so it is not written into a Seatbelt profile", s)
	}
	var b strings.Builder
	b.Grow(len(s) + 2 + strings.Count(s, `\`) + strings.Count(s, `"`))
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' || c == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String(), nil
}

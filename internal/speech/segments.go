package speech

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func Segments(text string, first, max int) []string {
	if max <= 0 {
		max = 1 << 30
	}
	if first <= 0 || first > max {
		first = max
	}
	var out []string
	var cur strings.Builder
	curLen, limit := 0, first
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
		curLen = 0
		limit = max
	}
	for _, sentence := range sentences(text) {
		n := utf8.RuneCountInString(strings.TrimRightFunc(sentence, unicode.IsSpace))
		if n > limit {
			flush()
			out = append(out, splitLong(sentence, max)...)
			continue
		}
		if curLen > 0 && curLen+utf8.RuneCountInString(sentence) > limit {
			flush()
		}
		cur.WriteString(sentence)
		curLen += utf8.RuneCountInString(sentence)
	}
	flush()
	// .
	// .
	// .
	// .
	if len(out) > 1 && utf8.RuneCountInString(out[0]) < minOpening &&
		utf8.RuneCountInString(out[0])+1+utf8.RuneCountInString(out[1]) <= max {
		out[1] = out[0] + " " + out[1]
		out = out[1:]
	}
	return out
}

// .
const minOpening = 20

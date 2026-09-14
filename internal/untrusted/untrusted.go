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
// .
// .
// .
// .
// .
// .
// .
// .
// .
package untrusted

import "strings"

// .
// .
// .
// .
const (
	Open  = "[[[EXTERNAL_UNTRUSTED_CONTENT]]]"
	Close = "[[[END_EXTERNAL_UNTRUSTED_CONTENT]]]"

	// .
	// .
	// .
	// .
	forgedNotice = "(forged sentinel removed)"
)

// .
// .
// .
// .
// .
// .
// .
func Wrap(source, content string) string {
	head := Open
	if s := scrub(source); s != "" {
		head = Open + " source: " + s
	}
	return head + "\n" + scrub(content) + "\n" + Close
}

// .
// .
// .
// .
func scrub(s string) string {
	s = strings.ReplaceAll(s, Close, forgedNotice)
	s = strings.ReplaceAll(s, Open, forgedNotice)
	return s
}

// .
// .
func Contains(s string) bool {
	return strings.Contains(s, Open) || strings.Contains(s, Close)
}

package speecheval

import (
	"strings"
	"testing"
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
func TestNormalizeNeverEmitsAnEmptyToken(t *testing.T) {
	for _, in := range []string{
		"٣٤٥",
		"१२३",
		"੫",
		"port ٣٤ open",
		"8180",
		"",
		"   ",
		"...---...",
	} {
		for i, tok := range Normalize(in) {
			if tok == "" {
				t.Errorf("Normalize(%q) emitted an empty token at index %d: %q", in, i, Normalize(in))
			}
		}
	}
	// .
	if got := strings.Join(Normalize("8180"), " "); got != "eight one eight zero" {
		t.Fatalf("ASCII digits = %q", got)
	}
	// .
	if got := Normalize("٣"); len(got) != 1 || got[0] != "٣" {
		t.Fatalf("non-ASCII numeral became %q, want it kept as one token", got)
	}
}

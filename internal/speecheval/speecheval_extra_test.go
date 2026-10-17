package speecheval

import (
	"strings"
	"testing"
)

// THE SCORER MUST NEVER INVENT A TOKEN.
//
// unicode.IsDigit admits Devanagari, Arabic-Indic and a dozen other
// digit families; digitWords is keyed by ASCII runes. A Bengali numeral
// therefore satisfied isAllDigits, missed the map, and appended the
// zero value — an empty string entering the token stream as a word that
// is not there, shifting every alignment after it. A scorer that
// fabricates tokens reports confident numbers about a sentence nobody
// said.
func TestNormalizeNeverEmitsAnEmptyToken(t *testing.T) {
	for _, in := range []string{
		"٣٤٥",          // Arabic-Indic digits
		"१२३",          // Devanagari digits
		"੫",            // Gurmukhi
		"port ٣٤ open", // mixed with ASCII words
		"8180",         // the ASCII case still spells out
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
	// And the ASCII path still does its job.
	if got := strings.Join(Normalize("8180"), " "); got != "eight one eight zero" {
		t.Fatalf("ASCII digits = %q", got)
	}
	// A non-ASCII numeral is treated as a word, not silently dropped.
	if got := Normalize("٣"); len(got) != 1 || got[0] != "٣" {
		t.Fatalf("non-ASCII numeral became %q, want it kept as one token", got)
	}
}

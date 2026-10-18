package tokenestimate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEstimateRoundsUpUTF8Bytes(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"a":    1,
		"abc":  1,
		"abcd": 2,
		"世界":   2,
	}
	for text, want := range cases {
		if got := Estimate(text); got != want {
			t.Errorf("Estimate(%q) = %d, want %d", text, got, want)
		}
	}
}

// The wire form is what the admission counts: the text as a JSON string,
// newlines and quotes at their escaped length, a plain word plus its quotes.
func TestEstimateWireCountsTheEscapedForm(t *testing.T) {
	for _, text := range []string{"", "abcd", "line one\nline two \"quoted\"\n", strings.Repeat("- a short line\n", 200)} {
		b, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := EstimateWire(text), Estimate(string(b)); got != want {
			t.Fatalf("EstimateWire(%.20q) = %d, want the marshalled form's %d", text, got, want)
		}
	}
	text := strings.Repeat("- a short line\n", 200)
	if got, raw := EstimateWire(text), Estimate(text); got <= raw {
		t.Fatalf("the wire form costs %d, not more than the raw %d", got, raw)
	}
}

package tools

import "testing"

// A NEW PLUGIN'S ID MAY NOT BEGIN WITH ONE OF THE HOST'S WORDS.
// Every word in the list is reserved, as the whole id and as its first
// segment, and the product's name in its dotted, dashed and underscored
// forms with it; an id that only contains such a word elsewhere, or one
// that begins with a longer word, is not.
func TestEveryReservedWordIsReservedAsAnIdAndItsFirstSegment(t *testing.T) {
	for _, w := range ReservedIDWords {
		for _, id := range []string{w, w + ".example", w + "-example", w + "_example"} {
			if got := ReservedPluginID(id); got != w {
				t.Errorf("%q: reserved as %q, want %q", id, got, w)
			}
		}
	}
	for _, id := range []string{"aii-os", "aii.os", "aii_os", "aiios", "host.on-behalf-of-your-operator"} {
		if ReservedPluginID(id) == "" {
			t.Errorf("%q is not reserved", id)
		}
	}
	for _, id := range []string{"id.aiii.voice", "org.example.memory", "org.example.host", "hostess", "aiii.tools", "systems", "my-operator"} {
		if got := ReservedPluginID(id); got != "" {
			t.Errorf("%q was reserved as %q", id, got)
		}
	}
}

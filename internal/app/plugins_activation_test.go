package app

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
func TestTheActivationLineSeparatesTheEnvelopeFromTheGrant(t *testing.T) {
	for _, c := range []struct {
		caps    []string
		granted bool
		want    []string
	}{
		// .
		// .
		// .
		// .
		{[]string{"fs.private"}, false, []string{"signed for fs.private", "needs no grant and is in effect", "quarantine, no operator grant"}},
		{[]string{"fs.private", "ring4.kv"}, false, []string{"signed for fs.private, ring4.kv", "(fs.private needs no grant and is in effect)"}},
		{[]string{"ring4.kv", "ring4.memory"}, true, []string{"signed for ring4.kv, ring4.memory", "brokered (operator grant active)"}},
		{nil, false, []string{"signed for nothing", "quarantine, no operator grant"}},
	} {
		// .
		// .
		// .
		if len(c.caps) > 0 && c.caps[0] != "fs.private" && strings.Contains(activationPosture(c.caps, c.granted), "in effect") {
			t.Fatalf("caps=%v: no grantable capability may be called in effect: %q", c.caps, activationPosture(c.caps, c.granted))
		}
		got := activationPosture(c.caps, c.granted)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Fatalf("caps=%v granted=%v: line %q lacks %q", c.caps, c.granted, got, want)
			}
		}
		if strings.Contains(got, "zero capabilities") {
			t.Fatalf("the misleading phrase is gone: %q", got)
		}
	}
}

package prompt

import (
	"strings"
	"testing"
)

// The opening greets a named identity by name; an unnamed one is told
// how a name is recorded, never "You are Unnamed."
func TestTheOpeningTellsAnUnnamedIdentityHowToRecordItsName(t *testing.T) {
	var named Composer
	named.SetName("Walker")
	if s := named.buildOpening(); !strings.Contains(s, "You are Walker.") || strings.Contains(s, "data/ui/name") {
		t.Fatalf("a named identity is greeted by name and needs no hint: %.120q", s)
	}
	for _, n := range []string{"Unnamed", ""} {
		var c Composer
		c.SetName(n)
		s := c.buildOpening()
		if strings.Contains(s, "You are Unnamed") || !strings.Contains(s, "data/ui/name") {
			t.Fatalf("name %q: the opening must say how a name is recorded, not call them Unnamed: %.160q", n, s)
		}
	}
}

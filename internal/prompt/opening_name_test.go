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

// An identity whose record holds no name and who wrote one to NameFile
// is told both truths: the record holds none, and the name they chose is
// the one the operator sees. The file never says who a named identity is.
func TestAnUnnamedIdentityThatChoseANameIsToldTheRecordHoldsNoneAndTheOperatorSeesIt(t *testing.T) {
	const told = "Your record holds no name. The name you chose, Aria, is the one your operator sees.\n\n"
	for _, n := range []string{"Unnamed", ""} {
		var c Composer
		c.SetName(n)
		c.SetChosenName(func() string { return "Aria" })
		s := c.buildOpening()
		if !strings.HasPrefix(s, told) || strings.Contains(s, "You are Aria") || strings.Contains(s, NameFile) {
			t.Fatalf("name %q with a chosen name: the opening must say the record holds none and the operator sees Aria: %.160q", n, s)
		}
		c.SetChosenName(func() string { return "" })
		if s := c.buildOpening(); !strings.Contains(s, NameFile) || strings.Contains(s, "Your record holds no name") {
			t.Fatalf("name %q with none chosen: the opening says how a name is recorded: %.160q", n, s)
		}
	}
	var named Composer
	named.SetName("Walker")
	named.SetChosenName(func() string { return "Aria" })
	if s := named.buildOpening(); !strings.HasPrefix(s, "You are Walker.\n\n") || strings.Contains(s, "Aria") {
		t.Fatalf("a recorded name is who the identity is; the file is display only: %.160q", s)
	}
}

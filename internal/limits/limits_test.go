package limits

import (
	"encoding/json"
	"testing"
)

// A CEILING IS A NUMBER OR "UNLIMITED", NEVER A SENTINEL. It reads and
// writes the number as the number it always was, and "unlimited" as the
// JSON string; anything else is refused. No limit allows every count and
// has no last round; zero is "not set".
func TestACeilingIsANumberOrUnlimitedAndNothingElse(t *testing.T) {
	for in, want := range map[string]Ceiling{`30`: Of(30), `0`: {}, `-1`: Of(-1), `"unlimited"`: Unlimited} {
		var c Ceiling
		if err := json.Unmarshal([]byte(in), &c); err != nil || c != want {
			t.Errorf("%s read as %+v (%v), want %+v", in, c, err, want)
		}
		out, err := json.Marshal(c)
		if err != nil || string(out) != in {
			t.Errorf("%+v written as %s (%v), want %s", c, out, err, in)
		}
	}
	for _, in := range []string{`"Unlimited"`, `"30"`, `1.5`, `true`, `null`, `"none"`} {
		var c Ceiling
		if err := json.Unmarshal([]byte(in), &c); err == nil && in != `null` {
			t.Errorf("%s was taken as %+v", in, c)
		}
	}
	if !Unlimited.Allows(1<<62) || Unlimited.Last(0) || Unlimited.String() != "unlimited" || Unlimited.IsZero() {
		t.Fatalf("no limit binds: %+v", Unlimited)
	}
	if c := Of(3); !c.Allows(2) || c.Allows(3) || !c.Last(2) || c.Last(1) || c.String() != "3" {
		t.Fatalf("a ceiling of 3 is not three: %+v", c)
	}
	if !(Ceiling{}).IsZero() || (Ceiling{}).Or(Of(7)) != Of(7) || Of(2).Or(Of(7)) != Of(2) || Unlimited.Or(Of(7)) != Unlimited {
		t.Fatal("not set does not take the next level, or a set value does")
	}
	if Of(5).AtMost(Unlimited) != Of(5) || Unlimited.AtMost(Of(5)) != Of(5) || Of(5).AtMost(Of(3)) != Of(3) || Unlimited.AtMost(Unlimited) != Unlimited {
		t.Fatal("the lower of two ceilings is not the one that binds first")
	}
	if !Unlimited.Above(Of(1<<62)) || Of(1<<62).Above(Unlimited) || Unlimited.Above(Unlimited) || !Of(4).Above(Of(3)) {
		t.Fatal("no limit is not above every number")
	}
}

// AN OMITTED CEILING STAYS OMITTED: a field tagged omitzero is left out
// when not set, so a file that had none is written back without one.
func TestANotSetCeilingIsLeftOutOfTheFile(t *testing.T) {
	out, err := json.Marshal(struct {
		A Ceiling `json:"a,omitzero"`
		B Ceiling `json:"b"`
	}{})
	if err != nil || string(out) != `{"b":0}` {
		t.Fatalf("written as %s (%v)", out, err)
	}
}

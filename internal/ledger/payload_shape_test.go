package ledger

import (
	"strings"
	"testing"
)

// A SHAPE IS MEMBERS AND KINDS, NEVER VALUES: two payloads with the same
// members and kinds share a digest whatever they say; a member added, or
// a kind changed, is another shape; and each kind reads as its name.
func TestAShapeIsMembersAndKindsNeverValues(t *testing.T) {
	a := `{"id":"exp_a","content":"first words","n":3,"f":0.5,"ok":true,"none":null,"refs":[{"kind":"belief","id":"b1"}],"tags":[],"meta":{}}`
	b := `{"id":"exp_zz","content":"other words entirely","n":-91,"f":12.25,"ok":false,"none":null,"refs":[{"kind":"experience","id":"e9"},{"kind":"belief","id":"b2"}],"tags":[],"meta":{}}`
	sa, err := ShapeOf([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	sb, err := ShapeOf([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if sa.Digest() != sb.Digest() {
		t.Fatalf("one shape, two digests:\n%s\n--\n%s", sa, sb)
	}
	want := "content string\nf number\nid string\nmeta object\nn integer\nnone null\nok bool\nrefs[].id string\nrefs[].kind string\ntags array"
	if sa.String() != want {
		t.Fatalf("the shape reads\n%s\nwant\n%s", sa, want)
	}
	for _, v := range []string{"first words", "exp_a", "belief"} {
		if strings.Contains(sa.String(), v) {
			t.Fatalf("a value is in the shape: %q", v)
		}
	}
	for name, other := range map[string]string{
		"a member added":           strings.Replace(a, `"id":"exp_a"`, `"id":"exp_a","why":"x"`, 1),
		"a kind changed":           strings.Replace(a, `"n":3`, `"n":"3"`, 1),
		"an integer made a number": strings.Replace(a, `"n":3`, `"n":3.5`, 1),
	} {
		so, err := ShapeOf([]byte(other))
		if err != nil {
			t.Fatal(err)
		}
		if so.Digest() == sa.Digest() {
			t.Errorf("%s: the digest did not move", name)
		}
	}
	if _, err := ShapeOf([]byte(`[1,2]`)); err == nil {
		t.Error("a payload that is not an object was given a shape")
	}
}

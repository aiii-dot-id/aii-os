package canonicaljson

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// .
// .
// .
// .

func TestCanonicalizeV1Vectors(t *testing.T) {
	vectors := []struct{ in, want string }{
		// .
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		// .
		{`{ "a" : 1 }`, `{"a":1}`},
		// .
		{`{"u":"<>&"}`, `{"u":"<>&"}`},
		// .
		// .
		// .
		{`{"n":1.5}`, `{"n":1.5}`},
		{`{"n":-0.25}`, `{"n":-0.25}`},
		// .
		{`{"n":9007199254740993}`, `{"n":9007199254740993}`},
		// .
		{`{"z":{"y":[3,1,{"k":"v"}],"x":true}}`, `{"z":{"x":true,"y":[3,1,{"k":"v"}]}}`},
		// .
		// .
		{"{\"s\":\"héllo ✓\", \"e\":\"\\u0041\"}", `{"e":"A","s":"héllo ✓"}`},
	}

	for _, v := range vectors {
		got, err := CanonicalizeV1([]byte(v.in))
		if err != nil {
			t.Errorf("canonicalize(%s): %v", v.in, err)
			continue
		}
		if string(got) != v.want {
			t.Errorf("canonicalize(%s)\n  got  %s\n  want %s", v.in, got, v.want)
		}
	}
}

func TestCanonicalizeV1Idempotent(t *testing.T) {
	in := []byte(`{"b":[{"z":1.5,"a":"<>&"}],"a":9007199254740993}`)
	first, err := CanonicalizeV1(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalizeV1(first)
	if err != nil {
		t.Fatalf("canonical output must itself be canonicalizable: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("not idempotent:\n  %s\n  %s", first, second)
	}
}

func TestCanonicalizeV1RejectsInvalid(t *testing.T) {
	for _, bad := range []string{
		``,
		`{`,
		`{"a":1,}`,
		`{"a":01}`,
		`{"a":1.}`,
		`{"a":1.0}`,
		`{"a":1.50}`,
		`{"a":1e2}`,
		`{"a":1E2}`,
		`{"a":1.5e3}`,
		`nul`,
		`"\ud800"`,
		`{"a":"\udc00"}`,
	} {
		if _, err := CanonicalizeV1([]byte(bad)); err == nil {
			t.Errorf("expected rejection of %q", bad)
		}
	}
}

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
func FuzzCanonicalizeV1(f *testing.F) {
	seeds := []string{
		`{}`, `[]`, `1`, `"s"`, `true`, `null`,
		`{"a":1,"b":[1,2,3],"c":{"d":"e"}}`,
		`{"html":"<script>&</script>"}`,
		`{"big":9007199254740993,"neg":-9007199254740994}`,
		`{"utf8":"日本語 ✓ ✗ — …"}`,
		`{"esc":"line\nbreak\ttab\"quote\\slash"}`,
		`{"u":"😀"}`,
		`{"sur":"\ud83d\ude00"}`,
		`[1,1.0,1e0,"1",true,null]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		canonical, err := CanonicalizeV1(data)
		if err != nil {
			return
		}

		// .
		again, err2 := CanonicalizeV1(data)
		if err2 != nil || !bytes.Equal(canonical, again) {
			t.Fatalf("nondeterministic: %q → %q vs %q (err %v)", data, canonical, again, err2)
		}

		// .
		fixed, err := CanonicalizeV1(canonical)
		if err != nil || !bytes.Equal(canonical, fixed) {
			t.Fatalf("not a fixed point: %q → %q (err %v)", canonical, fixed, err)
		}

		// .
		// .
		// .
		// .
		// .
		var a, b interface{}
		decA := json.NewDecoder(bytes.NewReader(data))
		decA.UseNumber()
		if err := decA.Decode(&a); err != nil {
			t.Fatalf("input rejected by encoding/json but accepted by CanonicalizeV1: %q", data)
		}
		decB := json.NewDecoder(bytes.NewReader(canonical))
		decB.UseNumber()
		if err := decB.Decode(&b); err != nil {
			t.Fatalf("canonical output is not valid JSON: %q → %q", data, canonical)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("meaning changed:\n  in  %q → %v\n  can %q → %v", data, a, canonical, b)
		}
	})
}

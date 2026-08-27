package canonicaljson

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// Known-vector pins: the canonical form is a SIGNED surface (payload_sha256
// in every authority bundle; the witness protocol hashes canonical bytes).
// Before writing this file, this function had ZERO direct tests — it guarded
// every signature in the system untested (P5, 2026-08-16).

func TestCanonicalizeV1Vectors(t *testing.T) {
	vectors := []struct{ in, want string }{
		// key ordering
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		// no whitespace
		{`{ "a" : 1 }`, `{"a":1}`},
		// no HTML escaping — signed bytes must not depend on Go's default escaper
		{`{"u":"<>&"}`, `{"u":"<>&"}`},
		// number preservation: no float64 round-trip; note the grammar —
		// no exponents, no trailing-zero fractions (those are REJECTED,
		// pinned below)
		{`{"n":1.5}`, `{"n":1.5}`},
		{`{"n":-0.25}`, `{"n":-0.25}`},
		// big ints stay exact (float64 would corrupt: 9007199254740993 → ...992)
		{`{"n":9007199254740993}`, `{"n":9007199254740993}`},
		// nested structures
		{`{"z":{"y":[3,1,{"k":"v"}],"x":true}}`, `{"z":{"x":true,"y":[3,1,{"k":"v"}]}}`},
		// escapes are minimal: \u0041 decodes to A and re-emits RAW;
		// raw UTF-8 passes through
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
		``,           // empty
		`{`,          // truncated
		`{"a":1,}`,   // trailing comma
		`{"a":01}`,   // leading zero
		`{"a":1.}`,   // bare fraction
		`{"a":1.0}`,  // trailing-zero fraction (grammar: rejected, not normalized)
		`{"a":1.50}`, // trailing-zero fraction
		`{"a":1e2}`,  // exponents are not in the canonical grammar
		`{"a":1E2}`,
		`{"a":1.5e3}`,
		`nul`,            // truncated literal
		`"\ud800"`,       // lone high surrogate
		`{"a":"\udc00"}`, // lone low surrogate
	} {
		if _, err := CanonicalizeV1([]byte(bad)); err == nil {
			t.Errorf("expected rejection of %q", bad)
		}
	}
}

// FuzzCanonicalizeV1: the signed-surface invariants, held against arbitrary
// input. Run with: go test ./internal/canonicaljson -fuzz FuzzCanonicalizeV1
//
// Invariants:
//  1. DETERMINISM — same input, same bytes, every call.
//  2. IDEMPOTENCE — canonical output re-canonicalizes to itself (it is a
//     fixed point; otherwise two verifiers hashing "the canonical form"
//     could disagree).
//  3. SEMANTIC ROUND-TRIP — canonicalization changes form, never meaning:
//     unmarshal(input) ≡ unmarshal(canonical).
func FuzzCanonicalizeV1(f *testing.F) {
	seeds := []string{
		`{}`, `[]`, `1`, `"s"`, `true`, `null`,
		`{"a":1,"b":[1,2,3],"c":{"d":"e"}}`,
		`{"html":"<script>&</script>"}`,
		`{"big":9007199254740993,"neg":-9007199254740994}`,
		`{"utf8":"日本語 ✓ ✗ — …"}`,
		`{"esc":"line\nbreak\ttab\"quote\\slash"}`,
		`{"u":"😀"}`,                 // astral plane as UTF-8
		`{"sur":"\ud83d\ude00"}`,    // astral plane as surrogate pair
		`[1,1.0,1e0,"1",true,null]`, // distinct values that must stay distinct
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		canonical, err := CanonicalizeV1(data)
		if err != nil {
			return // rejection is a valid outcome; the vector tests pin rejections
		}

		// 1. determinism
		again, err2 := CanonicalizeV1(data)
		if err2 != nil || !bytes.Equal(canonical, again) {
			t.Fatalf("nondeterministic: %q → %q vs %q (err %v)", data, canonical, again, err2)
		}

		// 2. idempotence (fixed point)
		fixed, err := CanonicalizeV1(canonical)
		if err != nil || !bytes.Equal(canonical, fixed) {
			t.Fatalf("not a fixed point: %q → %q (err %v)", canonical, fixed, err)
		}

		// 3. semantic round-trip — oracle decodes with UseNumber: the
		// canonicalizer preserves arbitrary-precision numbers verbatim
		// (found by the fuzzer: a 310-digit integer is valid JSON that
		// float64-default decoding cannot represent — the oracle must
		// test JSON semantics, not Go's float64 accident)
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

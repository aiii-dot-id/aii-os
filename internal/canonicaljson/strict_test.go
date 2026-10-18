package canonicaljson

import (
	"encoding/json"
	"errors"
	"testing"
)

// One value, every field declared, nothing after it but white space. The
// stray closers are the inputs Decode-then-More() let through.
func TestDecodeStrictReachesTheEndOfTheInput(t *testing.T) {
	type doc struct {
		A int `json:"a"`
	}
	for _, c := range []struct {
		in       string
		trailing bool
		bad      bool
	}{
		{in: `{"a":1}`},
		{in: "{\"a\":1}\n  \t"},
		{in: `{"a":1}}`, trailing: true},
		{in: `{"a":1}]`, trailing: true},
		{in: `{"a":1} {"a":2}`, trailing: true},
		{in: `{"a":1} x`, trailing: true},
		{in: `{"a":1,"b":2}`, bad: true},
		{in: `{"a":`, bad: true},
	} {
		var d doc
		err := DecodeStrict([]byte(c.in), &d)
		switch {
		case c.trailing && !errors.Is(err, ErrTrailingContent):
			t.Errorf("%q: want trailing content refused, got %v", c.in, err)
		case c.bad && (err == nil || errors.Is(err, ErrTrailingContent)):
			t.Errorf("%q: want a decode refusal, got %v", c.in, err)
		case !c.trailing && !c.bad && (err != nil || d.A != 1):
			t.Errorf("%q: want a=1, got %+v %v", c.in, d, err)
		}
	}
	var n struct {
		V any `json:"v"`
	}
	if err := DecodeStrictNumbers([]byte(`{"v":12345678901234567890}`), &n); err != nil {
		t.Fatal(err)
	}
	if num, ok := n.V.(json.Number); !ok || num.String() != "12345678901234567890" {
		t.Fatalf("DecodeStrictNumbers did not keep the number as written: %#v", n.V)
	}
}

func TestDecodeStrictMatchesNamesExactlyAndOnce(t *testing.T) {
	type inner struct {
		Kind string `json:"kind"`
	}
	type Embedded struct {
		Note string `json:"note"`
	}
	type doc struct {
		Embedded
		Limit int             `json:"limit"`
		Inner inner           `json:"inner"`
		Tags  map[string]int  `json:"tags"`
		Any   any             `json:"any"`
		Raw   json.RawMessage `json:"raw"`
		List  []inner         `json:"list"`
		Num   json.Number     `json:"num"`
	}
	for _, c := range []struct {
		in string
		ok bool
	}{
		{`{"limit":5,"inner":{"kind":"a"},"note":"n","tags":{"A":1,"a":2},"any":{"x":1},"raw":{"Limit":1},"list":[{"kind":"b"}],"num":3}`, true},
		{`{"Limit":5}`, false},
		{`{"limit":5,"Limit":50}`, false},
		{`{"limit":5,"limit":50}`, false},
		{`{"inner":{"Kind":"a"}}`, false},
		{`{"list":[{"KIND":"b"}]}`, false},
		{`{"Note":"n"}`, false},
		{`{"any":{"x":1,"x":2}}`, false},
		{`{"tags":{"a":1,"a":2}}`, false},
	} {
		var d doc
		err := DecodeStrict([]byte(c.in), &d)
		if c.ok && err != nil {
			t.Errorf("%s: refused: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: accepted as %+v", c.in, d)
		}
		if c.ok && (d.Limit != 5 || d.Inner.Kind != "a" || d.Note != "n" || d.Tags["A"] != 1 || d.List[0].Kind != "b" || string(d.Raw) != `{"Limit":1}`) {
			t.Errorf("%s: decoded %+v", c.in, d)
		}
	}
	// The numbers form keeps a number in an any as written.
	var n struct {
		Any any `json:"any"`
	}
	if err := DecodeStrictNumbers([]byte(`{"any":{"usage":0.1}}`), &n); err != nil {
		t.Fatal(err)
	}
	if v, ok := n.Any.(map[string]any)["usage"].(json.Number); !ok || v.String() != "0.1" {
		t.Fatalf("a number lost its form: %#v", n.Any)
	}
}

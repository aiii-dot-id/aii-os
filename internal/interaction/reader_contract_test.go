package interaction

import (
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

func code(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("untyped refusal: %v", err)
	}
	return e.Code
}

// A DATE FILTER IS ONE INSTANT, WHATEVER OFFSET NAMED IT. The store
// compares stored times as strings, so the normalized form must be UTC
// and fixed-width, and its string order must be time order; an interval
// that selects nothing, or contradicts itself, is refused rather than
// answered with nothing.
func TestADateFilterNormalizesToOneComparableInstant(t *testing.T) {
	q := Query{Version: 1, Filter: Filter{From: "2026-10-01T12:00:00+02:00", Until: "2026-10-01T10:00:00.5Z"}}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q.Filter.From != "2026-10-01T10:00:00.000000000Z" || q.Filter.Until != "2026-10-01T10:00:00.500000000Z" {
		t.Fatalf("normalized to %q .. %q", q.Filter.From, q.Filter.Until)
	}

	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var texts []string
	for _, d := range []time.Duration{0, 1, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 10 * time.Hour, 400 * 24 * time.Hour} {
		v, err := NormalizeTime(base.Add(d))
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, v)
	}
	if !sort.StringsAreSorted(texts) {
		t.Fatalf("string order is not time order: %v", texts)
	}
	for _, v := range texts {
		if len(v) != len(TimeLayout) {
			t.Fatalf("%q is not fixed-width", v)
		}
	}

	for _, c := range []struct {
		name       string
		from, till string
		undated    bool
	}{
		{"an equal interval", "2026-10-01T10:00:00Z", "2026-10-01T12:00:00+02:00", false},
		{"a reversed interval", "2026-10-02T00:00:00Z", "2026-10-01T00:00:00Z", false},
		{"undated with an interval", "2026-10-01T00:00:00Z", "", true},
		{"a date that is not RFC3339", "2026-10-01", "", false},
		{"an offset past year 9999", "9999-12-31T23:30:00-01:00", "", false},
	} {
		q := Query{Version: 1, Filter: Filter{From: c.from, Until: c.till, Undated: c.undated}}
		if err := q.Normalize(); err == nil || code(t, err) != "INTERACTION_ARGUMENT_INVALID" {
			t.Errorf("%s: %v, want a typed refusal", c.name, err)
		}
	}
}

// A CURSOR CONTINUES ITS OWN QUERY IN ITS OWN SOURCE, OR NOTHING. Plugins
// act on these codes: a retired source means start again, anything else
// is the caller's mistake.
func TestACursorContinuesOnlyItsOwnQuery(t *testing.T) {
	q := Query{Version: 1, Filter: Filter{Kinds: []Kind{Message}}}
	next := q
	next.Cursor = q.Next("inc-A", 42)
	before, err := next.Before("inc-A")
	if err != nil || before == nil || *before != 42 {
		t.Fatalf("the round trip lost its place: %v %v", before, err)
	}
	if b, err := q.Before("inc-A"); b != nil || err != nil {
		t.Fatalf("no cursor is the first page: %v %v", b, err)
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	other := q
	other.Filter.Kinds = []Kind{Notice}
	// The malformed rows carry the right scope, so each is refused for
	// its own fault and not for belonging to another query.
	plain := Query{Version: 1}.scope()
	for _, c := range []struct {
		name, want, says string
		q                Query
		inc              string
	}{
		{"a retired source", "INTERACTION_SOURCE_CHANGED", "retired source", next, "inc-B"},
		{"another query", "INTERACTION_ARGUMENT_INVALID", "another query", Query{Version: 1, Filter: other.Filter, Cursor: next.Cursor}, "inc-A"},
		{"bytes that are not base64", "INTERACTION_ARGUMENT_INVALID", "invalid cursor", Query{Version: 1, Cursor: "!!"}, "inc-A"},
		{"an unknown field", "INTERACTION_ARGUMENT_INVALID", "unknown", Query{Version: 1, Cursor: enc(`{"before":"1","extra":1,"incarnation":"inc-A","scope":"` + plain + `"}`)}, "inc-A"},
		{"a duplicated key", "INTERACTION_ARGUMENT_INVALID", "duplicate", Query{Version: 1, Cursor: enc(`{"before":"1","before":"2","incarnation":"inc-A","scope":"` + plain + `"}`)}, "inc-A"},
		{"a sequence that overflows", "INTERACTION_ARGUMENT_INVALID", "cursor sequence", Query{Version: 1, Cursor: enc(`{"before":"99999999999999999999","incarnation":"inc-A","scope":"` + plain + `"}`)}, "inc-A"},
	} {
		_, err := c.q.Before(c.inc)
		if err == nil || code(t, err) != c.want || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want %s naming %q", c.name, err, c.want, c.says)
		}
	}
}

// A DETAIL READ NAMES EXACTLY THE BYTES IT WAS PROMISED. Its bounds are
// the plugin wire's, and a detail that changed since it was advertised
// is a retired source, not a short read.
func TestADetailReadHoldsItsBounds(t *testing.T) {
	raw := []byte(strings.Repeat("x", 100))
	ok := ReadRequest{Version: 1, ID: "r1", Incarnation: "inc-A", SHA256: Hash(raw)}
	r := ok
	if err := ValidateRead(&r); err != nil || r.Length != MaxReadBytes || r.Source != "recorded" {
		t.Fatalf("defaults: %+v %v", r, err)
	}
	for _, c := range []struct {
		name, want string
		mutate     func(*ReadRequest)
	}{
		{"another version", "INTERACTION_VERSION_UNSUPPORTED", func(r *ReadRequest) { r.Version = 2 }},
		{"an unknown source", "INTERACTION_ARGUMENT_INVALID", func(r *ReadRequest) { r.Source = "elsewhere" }},
		{"no ID", "INTERACTION_ARGUMENT_INVALID", func(r *ReadRequest) { r.ID = "" }},
		{"a short hash", "INTERACTION_ARGUMENT_INVALID", func(r *ReadRequest) { r.SHA256 = "abc" }},
		{"a negative offset", "INTERACTION_ARGUMENT_INVALID", func(r *ReadRequest) { r.Offset = -1 }},
		{"a length over the bound", "INTERACTION_ARGUMENT_INVALID", func(r *ReadRequest) { r.Length = MaxReadBytes + 1 }},
	} {
		r := ok
		c.mutate(&r)
		if err := ValidateRead(&r); err == nil || code(t, err) != c.want {
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		}
	}

	r = ok
	r.Offset, r.Length = 90, 50
	res, err := DetailRead(r, raw)
	if err != nil || res.Bytes != 10 || !res.EOF || res.Size != 100 {
		t.Fatalf("a read across the end: %+v %v", res, err)
	}
	r.Offset = 100
	if res, err := DetailRead(r, raw); err != nil || res.Bytes != 0 || !res.EOF {
		t.Fatalf("a read at the end is empty and final: %+v %v", res, err)
	}
	r.Offset = 101
	if _, err := DetailRead(r, raw); err == nil || code(t, err) != "INTERACTION_ARGUMENT_INVALID" {
		t.Fatalf("a read past the end: %v", err)
	}
	r.Offset = 0
	if _, err := DetailRead(r, append(raw, 'y')); err == nil || code(t, err) != "INTERACTION_SOURCE_CHANGED" {
		t.Fatalf("a changed detail: %v", err)
	}
}

package store

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestReview3AmbiguousLabelMatchesCount(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_nr")
	mustDeliver(t, s, "ws_nr", "unserved: never ran", EvidenceNotRun, "")
	m := byName(mustMeasure(t, s))["ambiguous_awaiting_inspection"]
	if m.Numerator != 0 {
		t.Fatalf("settled not_run must not count as ambiguous: %d", m.Numerator)
	}
	// .
	// .
	o, c := strings.Index(m.Definition, "("), strings.Index(m.Definition, ")")
	if o < 0 || c <= o {
		t.Fatalf("ambiguous definition lost its class list: %q", m.Definition)
	}
	list := m.Definition[o:c]
	if strings.Contains(list, "not-run") || strings.Contains(list, "not_run") {
		t.Fatalf("the ambiguous class list must not include not_run: %q", list)
	}
	// .
	v := byName(mustMeasure(t, s))["deliveries_reached_verification"]
	if !strings.Contains(v.Definition, "classified") {
		t.Fatalf("the verification ratio must say it is over classified deliveries: %q", v.Definition)
	}
}

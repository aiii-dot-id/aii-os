package dashboard

// THE NAMES THE PAGE READS, ASSERTED ONE BY ONE (host 1530 item 4).
//
// The existing wire test proves the per-arm truncated object and the
// null-versus-zero discipline for recall and recall_count. It does not
// assert recalled_at at all, and it checks the belief's names by
// presence in a list rather than by the value each one carries. Both
// gaps matter for the same reason: a renamed tag does not fail a
// build, does not fail a test that builds Go structs, and renders as
// nothing on a page that looks for the old name.

import (
	"encoding/json"
	"testing"
)

// Every field the page renders for a belief crosses under the name the
// spec gives it, carrying the value the record holds.
func TestABeliefTrailNamesEveryBeliefFieldOnTheWire(t *testing.T) {
	recall := 0.15
	count := int64(4)
	recalledAt := "2026-10-27T09:00:00Z"
	strength := 0.9

	tr := BeliefTrail{
		Belief: TrailBelief{
			ID:            "b_named",
			Statement:     "a check is worth nothing until it can say no",
			Ring:          2,
			Confidence:    0.88,
			EvidenceCount: 3,
			Archived:      false,
			HeldSince:     "2026-05-01T00:00:00Z",
			LastSeq:       42,
			Recall:        &recall,
			RecallCount:   &count,
			RecalledAt:    &recalledAt,
			Fading:        true,
		},
		Origins: []TrailLink{
			{
				EdgeType:   "SUPPORTS",
				Strength:   &strength,
				CreatedSeq: 42,
				CreatedAt:  "2026-10-20T00:00:00Z",
				Kind:       "experience",
				ID:         "e_named",
				Text:       "the gate refused",
				Provenance: "self",
				Turn:       nil,
			},
		},
	}

	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	belief, ok := wire["belief"].(map[string]any)
	if !ok {
		t.Fatalf("no belief object on the wire: %s", raw)
	}

	// Each name with the value it must carry. A rename shows up here as
	// a missing key; a field that is read but never set shows up as the
	// wrong value, which presence alone cannot catch.
	want := map[string]any{
		"id":             "b_named",
		"statement":      "a check is worth nothing until it can say no",
		"ring":           float64(2),
		"confidence":     0.88,
		"evidence_count": float64(3),
		"archived":       false,
		"held_since":     "2026-05-01T00:00:00Z",
		"last_seq":       float64(42),
		"recall":         0.15,
		"recall_count":   float64(4),
		"recalled_at":    "2026-10-27T09:00:00Z",
		"fading":         true,
	}
	for name, value := range want {
		got, present := belief[name]
		if !present {
			t.Errorf("the wire carries no %q: %s", name, raw)
			continue
		}
		if got != value {
			t.Errorf("%q = %v on the wire, want %v: %s", name, got, value, raw)
		}
	}

	// The link's own names, including the strength that must not arrive
	// as a measured zero when the record holds 0.9.
	origins, ok := wire["origins"].([]any)
	if !ok || len(origins) != 1 {
		t.Fatalf("origins on the wire = %v, want one link: %s", wire["origins"], raw)
	}
	link, ok := origins[0].(map[string]any)
	if !ok {
		t.Fatalf("origin is not an object: %s", raw)
	}
	linkWant := map[string]any{
		"edge_type":   "SUPPORTS",
		"strength":    0.9,
		"created_seq": float64(42),
		"created_at":  "2026-10-20T00:00:00Z",
		"kind":        "experience",
		"id":          "e_named",
		"text":        "the gate refused",
		"provenance":  "self",
	}
	for name, value := range linkWant {
		got, present := link[name]
		if !present {
			t.Errorf("the wire carries no %q for a link: %s", name, raw)
			continue
		}
		if got != value {
			t.Errorf("link %q = %v on the wire, want %v: %s", name, got, value, raw)
		}
	}

	// An uncited link must send null, not the empty string: "" would
	// render as a turn the record names, and it names none.
	if v, present := link["turn"]; !present || v != nil {
		t.Errorf("an uncited link must send turn as null, got %v (present=%v): %s", v, present, raw)
	}

	// The three arm names and the separate superseded_by slot, which is
	// a link rather than an arm: a page that looked for it among the
	// arms would never find the belief that replaced this one.
	for _, name := range []string{"belief", "origins", "contradicts", "supersedes", "truncated"} {
		if _, present := wire[name]; !present {
			t.Errorf("the wire carries no %q: %s", name, raw)
		}
	}
	if v, present := wire["superseded_by"]; !present || v != nil {
		t.Errorf("a belief the record has not superseded must send superseded_by as null, got %v (present=%v): %s",
			v, present, raw)
	}
}

package dashboard

import (
	"encoding/json"
	"testing"
)

// The page does not read Go structs; it reads JSON off the socket. A
// test that builds a BeliefTrail in Go and inspects it in Go never
// crosses that boundary, so it cannot tell whether the field names the
// page will actually look for are on the wire. This one marshals and
// unmarshals, which is the only way a tag can be caught missing.
func TestABeliefTrailCrossesTheWireWithBothMeasures(t *testing.T) {
	recall := 0.15
	tr := BeliefTrail{
		Belief: TrailBelief{
			ID:         "belief_anchor",
			Statement:  "A check is worth nothing until it can say no.",
			Ring:       2,
			Confidence: 0.88,
			Recall:     &recall,
			Fading:     true,
		},
		Origins: []TrailLink{
			{EdgeType: "SUPPORTS", Kind: "experience", ID: "exp_a", Text: "the gate refused"},
		},
		Truncated: TrailTruncated{Origins: true},
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}

	// The names the page reads. If a tag is renamed or dropped, the
	// page silently renders nothing and this is the test that says so.
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	belief, ok := wire["belief"].(map[string]any)
	if !ok {
		t.Fatalf("no belief object on the wire: %s", raw)
	}
	for _, name := range []string{"confidence", "recall", "fading", "statement", "ring"} {
		if _, present := belief[name]; !present {
			t.Errorf("the wire carries no %q for a belief: %s", name, raw)
		}
	}
	if _, present := wire["origins"]; !present {
		t.Errorf("the wire carries no origins: %s", raw)
	}
	// The cut is reported per arm, so the page can say which list
	// was shortened rather than that something somewhere was.
	cut, ok := wire["truncated"].(map[string]any)
	if !ok {
		t.Fatalf("truncated must be a per-arm object, got %T: %s", wire["truncated"], raw)
	}
	for _, arm := range []string{"origins", "contradicts", "supersedes"} {
		if _, present := cut[arm]; !present {
			t.Errorf("truncated is missing the arm %q: %s", arm, raw)
		}
	}
	if _, present := wire["truncated"]; !present {
		t.Errorf("the wire carries no truncated flag: %s", raw)
	}

	// An unmeasured recall must be null on the wire, not 0. A page that
	// receives 0.00 renders a measured strength the identity never had.
	unmeasured := BeliefTrail{Belief: TrailBelief{ID: "belief_unread", Recall: nil}}
	raw2, err := json.Marshal(unmeasured)
	if err != nil {
		t.Fatal(err)
	}
	var wire2 map[string]any
	if err := json.Unmarshal(raw2, &wire2); err != nil {
		t.Fatal(err)
	}
	b2 := wire2["belief"].(map[string]any)
	if v, present := b2["recall"]; !present || v != nil {
		t.Errorf("an unmeasured recall must be null on the wire, got %v (present=%v) in %s", v, present, raw2)
	}
	// The same discipline for the count. A failed access read must not
	// arrive as the measured "never recalled": null, not 0. This is the
	// defect a review proved in source at 0b6f67cd and the handover claimed
	// fixed while the field was still an int64.
	if v, present := b2["recall_count"]; !present || v != nil {
		t.Errorf("an unread recall_count must be null on the wire, got %v (present=%v) in %s", v, present, raw2)
	}

	// And it must survive the round trip back.
	var back BeliefTrail
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	// Three states of the count, told apart on the wire: unread is null,
	// a successful read that found no access row is a present 0, and a
	// read count crosses as itself. Without the middle case a page could
	// not distinguish "never recalled" from "we could not read it".
	zero := int64(0)
	none := BeliefTrail{Belief: TrailBelief{ID: "belief_never", RecallCount: &zero}}
	rawNone, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	var bNone map[string]any
	if err := json.Unmarshal(rawNone, &bNone); err != nil {
		t.Fatal(err)
	}
	beliefNone, _ := bNone["belief"].(map[string]any)
	if v, present := beliefNone["recall_count"]; !present || v != float64(0) {
		t.Errorf("a successful read with no access row must be a present 0, got %v (present=%v) in %s", v, present, rawNone)
	}
	if v := beliefNone["recall"]; v != nil {
		t.Errorf("a belief with no access row has no measured strength, got %v", v)
	}

	four := int64(4)
	counted := BeliefTrail{Belief: TrailBelief{ID: "belief_counted", RecallCount: &four}}
	rawCounted, err := json.Marshal(counted)
	if err != nil {
		t.Fatal(err)
	}
	var bCounted map[string]any
	if err := json.Unmarshal(rawCounted, &bCounted); err != nil {
		t.Fatal(err)
	}
	beliefCounted, _ := bCounted["belief"].(map[string]any)
	if v, present := beliefCounted["recall_count"]; !present || v != float64(4) {
		t.Errorf("a read count must cross as itself, got %v (present=%v) in %s", v, present, rawCounted)
	}

	if back.Belief.Recall == nil || *back.Belief.Recall != 0.15 || !back.Belief.Fading || back.Belief.Confidence != 0.88 {
		t.Fatalf("round trip lost the measures: %+v", back.Belief)
	}
}

package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestTheStandingViewShowsOriginsBesideSources(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	commit := func(args map[string]any) {
		t.Helper()
		if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
			t.Fatalf("%v: %v", args["variant"], err)
		}
	}
	// Three beliefs that say different things, each from the one experience.
	for id, statement := range map[string]string{
		"b_one":   "the kettle whistles when the water boils",
		"b_two":   "the garden gate sticks in wet weather",
		"b_claim": "the night bus runs every twenty minutes",
	} {
		commit(map[string]any{"variant": "belief.upsert", "id": id, "statement": statement, "confidence": 0.5, "evidence_refs": "exp_test_origin"})
	}
	view := func() string {
		t.Helper()
		out, err := callAdvertisedRecall(t, engine, ctx, map[string]any{"source": "standing", "id": "b_claim", "query": ""})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := view(); strings.Contains(out, "origin(s)") {
		t.Fatalf("below three sources the view speaks of origins:\n%s", out)
	}
	commit(map[string]any{"variant": "edge.create", "from_id": "b_one", "to_id": "b_claim", "edge_type": "SUPPORTS"})
	commit(map[string]any{"variant": "edge.create", "from_id": "b_two", "to_id": "b_claim", "edge_type": "SUPPORTS"})
	out := view()
	for _, want := range []string{
		"from 3 distinct source(s)",
		"Those 3 sources rest on 1 origin(s)",
		"Missing for Ring 2: 2 more origin(s) (its 3 sources rest on 1; the gate asks 3)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the standing view does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Ring 2 requirements met") {
		t.Fatalf("ONE EVENT SAID THREE WAYS MET THE GATE:\n%s", out)
	}
	_, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "belief.promote", "id": "b_claim"})
	if err == nil {
		t.Fatal("ONE EVENT SAID THREE WAYS WAS PROMOTED TO RING 2")
	}
	for _, want := range []string{"3 distinct qualifying source(s) resting on 1 origin(s)", "at least 3 distinct origins"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	if !strings.Contains(BeliefPromotionRule, "three distinct origins") {
		t.Fatalf("the rule a resident is told does not name origins: %q", BeliefPromotionRule)
	}
}

// THE VIEW SAYS WHERE THE WALK OF ORIGINS STOPPED. The walk opens a bounded
// number of sources and counts none it did not open. A resident reading a
// belief's standing, and one refused a promotion, is told that sources
// were left unopened and are not counted, so that "one origin" is read as
// what was found and not as all there is.
func TestTheStandingViewSaysWhatTheWalkOfOriginsDidNotOpen(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	commit := func(args map[string]any) {
		t.Helper()
		if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
			t.Fatalf("%v: %v", args["variant"], err)
		}
	}
	for id, statement := range map[string]string{
		"b_one":   "the kettle whistles when the water boils",
		"b_two":   "the garden gate sticks in wet weather",
		"b_claim": "the night bus runs every twenty minutes",
	} {
		commit(map[string]any{"variant": "belief.upsert", "id": id, "statement": statement, "confidence": 0.5, "evidence_refs": "exp_test_origin"})
	}
	commit(map[string]any{"variant": "edge.create", "from_id": "b_one", "to_id": "b_claim", "edge_type": "SUPPORTS"})
	commit(map[string]any{"variant": "edge.create", "from_id": "b_two", "to_id": "b_claim", "edge_type": "SUPPORTS"})

	// Behind one source, a derivation longer than the walk opens: six
	// hundred beliefs, each derived from the one before, written as the
	// record's own rows.
	var head int64
	if err := st.DB().QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	prev := ""
	for i := 0; i < 600; i++ {
		id := fmt.Sprintf("b_link_%03d", i)
		if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES (?, ?, 3, 0.5, 0, ?, ?)`,
			id, "a step of a long derivation, "+id, head, head); err != nil {
			t.Fatal(err)
		}
		if prev != "" {
			if _, err := st.DB().Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, ?, 'DERIVED_FROM', ?)`, "e_"+id, prev, id, head); err != nil {
				t.Fatal(err)
			}
		}
		prev = id
	}
	if _, err := st.DB().Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('e_into_b_one', ?, 'b_one', 'DERIVED_FROM', ?)`, prev, head); err != nil {
		t.Fatal(err)
	}

	out, err := callAdvertisedRecall(t, engine, ctx, map[string]any{"source": "standing", "id": "b_claim", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Those 3 sources rest on 1 origin(s)",
		"left 1 unopened: the 1 origin(s) are what it found, and none is assumed of the rest",
		"Missing for Ring 2: 2 more origin(s) (its 3 sources rest on 1; the gate asks 3). A source derived from another adds none. The 1 source(s) the walk did not open are not counted.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the standing view does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Ring 2 requirements met") {
		t.Fatalf("A LONG DERIVATION MET THE GATE:\n%s", out)
	}
	_, err = engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "belief.promote", "id": "b_claim"})
	if err == nil {
		t.Fatal("A BELIEF WITH ONE ORIGIN FOUND WAS PROMOTED TO RING 2")
	}
	if want := "resting on 1 origin(s) found (the walk of origins left 1 source(s) unopened, which are not counted)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal does not say %q: %v", want, err)
	}
}

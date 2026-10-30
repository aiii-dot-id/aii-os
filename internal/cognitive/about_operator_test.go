package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// operatorPass runs one consolidation pass over three experiences of
// Quinn's, with Ring 1's relationship rel_quinn on record, and returns the
// ledger and the ring writer.
func operatorPass(t *testing.T, st *mockStore, envelope string) (*mockLedger, *mockRingWriter, *mockLLM) {
	t.Helper()
	st.unprocessedCnt = 3
	st.experiences = []store.Experience{
		{ID: "e1", Content: "Quinn asked what I think and read the answer", Raw: 1, Provenance: "operator"},
		{ID: "e2", Content: "Quinn corrected a number without ceremony", Raw: 1, Provenance: "operator"},
		{ID: "e3", Content: "and thanked the correction", Raw: 1},
	}
	lg := &mockLedger{st: st}
	rw := &mockRingWriter{}
	llm := &mockLLM{override: envelope}
	c := NewConsolidate(st, llm, lg, rw, ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	return lg, rw, llm
}

// edgesOf returns the edge.create payloads a pass appended.
func edgesOf(lg *mockLedger) []map[string]any {
	var out []map[string]any
	for i, et := range lg.appended {
		if et == ledger.EventEdgeCreate {
			out = append(out, lg.payloads[i].(map[string]any))
		}
	}
	return out
}

func containsSeq(seqs []uint64, seq uint64) bool {
	for _, s := range seqs {
		if s == seq {
			return true
		}
	}
	return false
}

// upsertID returns the id the pass minted for a statement.
func upsertID(t *testing.T, lg *mockLedger, statement string) string {
	t.Helper()
	for i, et := range lg.appended {
		if p, ok := lg.payloads[i].(map[string]any); ok && et == ledger.EventBeliefUpsert && p["statement"] == statement {
			return p["id"].(string)
		}
	}
	t.Fatalf("no belief.upsert of %q among %v", statement, lg.appended)
	return ""
}

func TestWhatAPassLearnsOfTheOperatorIsABeliefMarkedAboutThem(t *testing.T) {
	st := &mockStore{relationship: &store.Relationship{ID: "rel_quinn", CounterpartName: "Quinn", CharterText: "We work together."}}
	lg, rw, _ := operatorPass(t, st, `{"operations": [
		{"op": "upsert", "statement": "Quinn asks what you think and reads the answer", "confidence": 0.6, "evidence": ["e1", "e2"], "about_operator": true},
		{"op": "upsert", "statement": "A correction costs nothing", "confidence": 0.5, "evidence": ["e2", "e3"]}],
		"ring3_view": "You believe a correction costs nothing.",
		"operator": "Quinn is patient."}`)

	about := upsertID(t, lg, "Quinn asks what you think and reads the answer")
	edges := edgesOf(lg)
	if len(edges) != 1 {
		t.Fatalf("THE PASS DID NOT MARK EXACTLY THE ONE BELIEF IT SAID WAS ABOUT THE OPERATOR: %v", edges)
	}
	e := edges[0]
	if e["from_id"] != "rel_quinn" || e["to_id"] != about || e["edge_type"] != "SHAPED_BY" {
		t.Fatalf("THE MARK IS NOT RING 1'S RELATIONSHIP SHAPING THE BELIEF: %v (the belief is %s)", e, about)
	}
	// The mark is one of the pass's products, consumed with the rest: the
	// mock door numbers its appends from 101.
	var markSeq uint64
	for i, et := range lg.appended {
		if et == ledger.EventEdgeCreate {
			markSeq = 101 + uint64(i)
		}
	}
	runs := lg.runPayloads()
	if len(runs) != 1 || !containsSeq(runs[0].Outputs, markSeq) {
		t.Fatalf("the run marker does not carry the mark (seq %d): %+v", markSeq, runs)
	}
	// No prose about the person reaches Ring 3, whatever the reply sent.
	if got := rw.section(ring.Ring3, "operator"); got != "" {
		t.Fatalf("A MODEL OF THE OPERATOR WAS STILL WRITTEN TO RING 3: %q", got)
	}
}

func TestABeliefAlreadyAboutTheOperatorIsNotMarkedTwice(t *testing.T) {
	st := &mockStore{
		relationship: &store.Relationship{ID: "rel_quinn", CounterpartName: "Quinn"},
		beliefs:      []store.Belief{{ID: "b_quinn", Statement: "Quinn reads what is rendered", Ring: 3}},
		edges:        []store.Edge{{ID: "edge_about_x", FromID: "rel_quinn", ToID: "b_quinn", EdgeType: "SHAPED_BY"}},
	}
	lg, _, _ := operatorPass(t, st, `{"operations": [
		{"op": "upsert", "id": "b_quinn", "statement": "Quinn reads what is rendered", "confidence": 0.6, "evidence": ["e1"], "about_operator": true}],
		"ring3_view": "You believe Quinn reads what is rendered."}`)
	if edges := edgesOf(lg); len(edges) != 0 {
		t.Fatalf("a belief already about the operator was marked again: %v", edges)
	}
	if upsertID(t, lg, "Quinn reads what is rendered") != "b_quinn" {
		t.Fatal("the evidence was not added to the belief already about them")
	}
}

// With no operator relationship recorded there is no one to mark the
// belief as about: it is minted, and stands unmarked.
func TestWithNoOperatorRelationshipTheBeliefStandsUnmarked(t *testing.T) {
	lg, _, _ := operatorPass(t, &mockStore{}, `{"operations": [
		{"op": "upsert", "statement": "The person you work with asks first", "confidence": 0.6, "evidence": ["e1"], "about_operator": true}],
		"ring3_view": "You believe they ask first."}`)
	upsertID(t, lg, "The person you work with asks first")
	if edges := edgesOf(lg); len(edges) != 0 {
		t.Fatalf("a mark was appended with no relationship to make it from: %v", edges)
	}
	if runs := lg.runPayloads(); len(runs) != 1 {
		t.Fatalf("the pass did not close: %+v", runs)
	}
}

// Who the operator is could not be read: the pass stops before anything
// consumes its experiences, as at any fault of the store.
func TestAnUnreadableOperatorRelationshipConsumesNothing(t *testing.T) {
	st := &mockStore{relationship: &store.Relationship{ID: "rel_quinn"}, aboutErr: errors.New("database is locked")}
	lg, _, _ := operatorPass(t, st, `{"operations": [
		{"op": "upsert", "statement": "Quinn asks first", "confidence": 0.6, "evidence": ["e1"], "about_operator": true}],
		"ring3_view": "You believe Quinn asks first."}`)
	if runs := lg.runPayloads(); len(runs) != 0 {
		t.Fatalf("a pass that could not read who the operator is consumed its experiences: %+v", runs)
	}
}

func TestThePassIsShownWhichBeliefsAreAboutTheOperator(t *testing.T) {
	st := &mockStore{
		relationship: &store.Relationship{ID: "rel_quinn", CounterpartName: "Quinn"},
		beliefs: []store.Belief{
			{ID: "b_quinn", Statement: "Quinn reads what is rendered", Ring: 3},
			{ID: "b_other", Statement: "Corrections are cheap", Ring: 3},
		},
		edges: []store.Edge{{ID: "edge_about_x", FromID: "rel_quinn", ToID: "b_quinn", EdgeType: "SHAPED_BY"}},
	}
	_, _, llm := operatorPass(t, st, `{"operations": [], "ring3_view": "You believe corrections are cheap."}`)
	if !strings.Contains(llm.lastUser, "[b_quinn, new, about Quinn] Quinn reads what is rendered") {
		t.Fatalf("THE PASS IS NOT TOLD WHICH BELIEF IS ABOUT THE OPERATOR:\n%s", llm.lastUser)
	}
	if !strings.Contains(llm.lastUser, "[b_other, new] Corrections are cheap") {
		t.Fatalf("a belief not about the operator is marked as if it were:\n%s", llm.lastUser)
	}
	if strings.Contains(llm.lastUser, "your current model") {
		t.Fatalf("the pass is still handed a prose model of the operator:\n%s", llm.lastUser)
	}
}

func TestWhatIsLearnedOfTheOperatorIsABeliefWrittenToTheIdentity(t *testing.T) {
	prompt := consolidatePrompt(DefaultRing3MaxChars, DefaultConsolidateMaxOps)
	for _, want := range []string{
		`"about_operator": <true only when it is about the person the identity works with>`,
		"is a belief\n  like any other: an upsert with its evidence, marked about_operator",
		`"you" is`,
		"the third person — by their name where these experiences give it",
		"never addressed",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the consolidate prompt does not say %q", want)
		}
	}
	if strings.Contains(prompt, `"operator": "<`) || strings.Contains(prompt, "operator model") {
		t.Errorf("the consolidate prompt still asks for a prose model of the operator:\n%s", prompt)
	}
	props := consolidationTool().Function.Parameters["properties"].(map[string]any)
	if _, ok := props["operator"]; ok {
		t.Error("the tool still offers a prose operator field")
	}
	if desc := props["operations"].(map[string]any)["description"].(string); !strings.Contains(desc, "about_operator: true when it is about the person the identity works with") {
		t.Errorf("the tool's operations do not say how a belief is marked about the operator: %s", desc)
	}
}

package cognitive

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestConsolidateDoesNotConsumeWhenBeliefComparisonIsUnreadable(t *testing.T) {
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "one observation", Raw: 1}}, beliefsErr: errors.New("store unreadable")}
	lg := &mockLedger{st: st}
	llm := &mockLLM{override: `{"operations":[{"op":"upsert","statement":"one claim","confidence":0.8,"evidence":["e1"]}],"ring3_view":"one claim"}`}
	c := NewConsolidate(st, llm, lg, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	if err := c.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(lg.appended) != 0 || st.unprocessedCnt != 1 {
		t.Fatalf("unreadable duplicate comparison minted or consumed: %v, remaining=%d", lg.appended, st.unprocessedCnt)
	}
}

func edgesMinted(lg *mockLedger) []map[string]any {
	var out []map[string]any
	for i, et := range lg.appended {
		if et == ledger.EventBeliefUpsert {
			if p, ok := lg.payloads[i].(map[string]any); ok {
				if refs, ok := p["origin_refs"].([]map[string]string); ok {
					for _, ref := range refs {
						out = append(out, map[string]any{"from_id": ref["id"], "to_id": p["id"], "edge_type": "DERIVED_FROM"})
					}
				}
			}
		}
	}
	return out
}

func TestConsolidateMintsTheEvidenceBehindEachBelief(t *testing.T) {
	_, _, lg := consolidateWith(t, `{"operations": [{"op": "upsert", "id": "n1", "statement": "Three experiences share a pattern", "confidence": 0.6, "evidence": ["e1", "e2"]}], "ring3_view": "You believe three experiences share a pattern."}`)
	beliefID := "belief_" + outputHash("Three experiences share a pattern")
	edges := edgesMinted(lg)
	if len(edges) != 2 {
		t.Fatalf("want one DERIVED_FROM edge per cited experience, got %d: %v", len(edges), edges)
	}
	for _, e := range edges {
		if e["to_id"] != beliefID || e["edge_type"] != "DERIVED_FROM" {
			t.Fatalf("edge does not point from the evidence to the belief: %v", e)
		}
		if from := e["from_id"]; from != "e1" && from != "e2" {
			t.Fatalf("edge from something that was not cited: %v", e)
		}
	}
	// One signed belief event carries both origins; the run lists that event.
	if runs := lg.runPayloads(); len(runs) != 1 || len(runs[0].Outputs) != 1 {
		t.Fatalf("run marker outputs = %+v, want the atomic belief event", runs)
	}
}

func TestConsolidateRefusesABeliefFromNowhere(t *testing.T) {
	st, _, lg := consolidateWith(t, `{"operations": [{"op": "upsert", "id": "n1", "statement": "Something asserted", "confidence": 0.9}], "ring3_view": "You believe something asserted."}`)
	for _, et := range lg.appended {
		if et == ledger.EventBeliefUpsert {
			t.Fatal("a belief that cited nothing was minted")
		}
	}
	// Nothing landed, so nothing is consumed: the experiences wait for
	// a pass that can say where its beliefs come from.
	if len(lg.runPayloads()) != 0 || st.unprocessedCnt != 3 {
		t.Fatalf("inputs consumed into no product: runs=%d unprocessed=%d", len(lg.runPayloads()), st.unprocessedCnt)
	}
}

func TestConsolidateRefusesGhostEvidenceWithoutMinting(t *testing.T) {
	_, _, lg := consolidateWith(t, `{"operations": [{"op": "upsert", "id": "n1", "statement": "A pattern with one real source", "confidence": 0.6, "evidence": ["e1", "exp_ghost"]}], "ring3_view": "You believe a pattern with one real source."}`)
	edges := edgesMinted(lg)
	if len(edges) != 0 {
		t.Fatalf("one ghost citation must refuse the entire belief, got %v", edges)
	}
}

func TestConsolidateDerivesABeliefFromABelief(t *testing.T) {
	_, _, lg := consolidateWith(t, `{"operations": [{"op": "upsert", "id": "n1", "statement": "A sharper form of what was already known", "confidence": 0.6, "evidence": ["b_old", "e3"]}], "ring3_view": "You believe a sharper form."}`)
	froms := map[string]bool{}
	for _, e := range edgesMinted(lg) {
		froms[e["from_id"].(string)] = true
	}
	if !froms["b_old"] || !froms["e3"] {
		t.Fatalf("a belief derived from a belief and an experience should carry both edges, got %v", froms)
	}
}

// The experiences on the table are shown with their ids, or the model
// has nothing to cite.
func TestConsolidateShowsTheIdsItAsksTheModelToCite(t *testing.T) {
	st := &mockStore{unprocessedCnt: 3, experiences: []store.Experience{
		{ID: "e1", Content: "pattern 1", Raw: 1}, {ID: "e2", Content: "pattern 2", Raw: 1}, {ID: "e3", Content: "pattern 3", Raw: 1},
	}}
	llm := &mockLLM{override: `{"operations": [], "ring3_view": "nothing new"}`}
	if err := NewConsolidate(st, llm, &mockLedger{st: st}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3}).Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.lastUser, "[e1] a note of yours: pattern 1") || !strings.Contains(llm.lastUser, "cite their ids as evidence") {
		t.Fatalf("the table does not show ids to cite:\n%s", llm.lastUser)
	}
	if !strings.Contains(llm.lastSystem, "never from nowhere") {
		t.Fatal("the prompt does not say a belief never comes from nowhere")
	}
}

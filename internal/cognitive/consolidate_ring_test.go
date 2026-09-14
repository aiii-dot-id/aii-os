package cognitive

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

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
// .
// .
// .
func TestConsolidateCannotRewriteAPromotedBeliefByRederivingItsID(t *testing.T) {
	const promoted = "Honesty above comfort"
	promotedID := "belief_" + outputHash(promoted)

	st := &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e1", Content: "held the line on an uncomfortable answer", Raw: 1},
			{ID: "e2", Content: "held it again under pressure", Raw: 1},
			{ID: "e3", Content: "and a third time", Raw: 1},
		},
		beliefs: []store.Belief{
			// .
			{ID: promotedID, Statement: promoted, Ring: 2},
		},
	}
	lg := &mockLedger{st: st}
	rw := &mockRingWriter{}
	// .
	c := NewConsolidate(st, &mockLLM{override: `{
		"operations": [
			{"op": "upsert", "statement": "Honesty above comfort", "confidence": 0.05, "evidence": ["e1", "e2", "e3"]}
		],
		"ring3_view": "You believe what stands."
	}`}, lg, rw, ConsolidateConfig{Threshold: 3})

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	for i, kind := range lg.appended {
		if kind == ledger.EventBeliefUpsert {
			p, _ := lg.payloads[i].(map[string]interface{})
			t.Fatalf("A RING 2 BELIEF WAS REWRITTEN BY A RING 3 MINT: upsert %+v — the conflict update rewrites "+
				"statement, confidence and last_seq, so the charter-adjacent claim changes while its ring survives", p)
		}
	}
	// .
	if len(lg.appended) != 1 || lg.appended[0] != ledger.EventConsolidationRun {
		t.Fatalf("only the run marker should land, got %v", lg.appended)
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("a pass whose only op dropped still consumes, %d remain", st.unprocessedCnt)
	}
}

// .
// .
// .
func TestConsolidateStillUpsertsAnUnpromotedStatement(t *testing.T) {
	st := &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e1", Content: "shipped at midnight", Raw: 1},
			{ID: "e2", Content: "shipped at midnight again", Raw: 1},
			{ID: "e3", Content: "a third midnight ship", Raw: 1},
		},
		beliefs: []store.Belief{{ID: "b_r3", Statement: "The operator works late", Ring: 3}},
	}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, &mockLLM{override: `{
		"operations": [
			{"op": "upsert", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["e1", "e2", "e3"]}
		],
		"ring3_view": "You believe the operator ships at midnight."
	}`}, lg, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	minted := false
	for _, kind := range lg.appended {
		if kind == ledger.EventBeliefUpsert {
			minted = true
		}
	}
	if !minted {
		t.Fatalf("working truth was refused: the guard must stop a promoted rewrite, not ordinary consolidation (%v)", lg.appended)
	}
}

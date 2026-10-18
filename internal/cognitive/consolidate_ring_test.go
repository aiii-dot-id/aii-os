package cognitive

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A belief keeps the id it was minted under when it is promoted, and
// CONSOLIDATE derives ids from the statement — so a later pass that
// re-emits a promoted statement recomputes the SAME id. The ring check
// used to sit in one branch of an if/else, and the derived-id branch was
// the other one: the mint went through unauthorised, and
// materializeBeliefUpsert's ON CONFLICT rewrites statement, confidence
// and last_seq (ring is not in its SET list, so it survives). Promoted
// identity would have kept its authority while its words changed
// underneath it.
//
// The model does not even have to aim at it. It only has to say the same
// thing twice, once before promotion and once after — which is what a
// belief worth promoting looks like.
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
			// Minted by an earlier consolidation, since promoted to Ring 2.
			{ID: promotedID, Statement: promoted, Ring: 2},
		},
	}
	lg := &mockLedger{st: st}
	rw := &mockRingWriter{}
	// No id on the op: the engine derives one, and it collides.
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
			p, _ := lg.payloads[i].(map[string]any)
			t.Fatalf("A RING 2 BELIEF WAS REWRITTEN BY A RING 3 MINT: upsert %+v — the conflict update rewrites "+
				"statement, confidence and last_seq, so the charter-adjacent claim changes while its ring survives", p)
		}
	}
	// The pass still closes: one bad op must not wedge the queue.
	if len(lg.appended) != 1 || lg.appended[0] != ledger.EventConsolidationRun {
		t.Fatalf("only the run marker should land, got %v", lg.appended)
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("a pass whose only op dropped still consumes, %d remain", st.unprocessedCnt)
	}
}

// The control, and the reason this is a guard rather than a wall: a
// statement the identity has NOT promoted is ordinary working truth, and
// consolidation owns it.
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

package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A CONTRADICTION IS RESOLVED WHEN THE BELIEF IT CAME FROM IS RETIRED, for
// standing as for the tensions item (a review, 2026-11-08, D1: the tension
// disappeared, but the belief stayed suspect and promotion refused it).
// Real archive and supersede events; the edge and the record are kept.
// One that comes from a current belief, or from an experience, still holds.
func TestAContradictionFromARetiredBeliefNoLongerMakesItsTargetSuspect(t *testing.T) {
	s, mat := reportStore(t)
	for i, from := range []string{"exp", "exp2", "exp3"} {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "sup" + string(rune('a'+i)), "from_id": from, "to_id": "claim", "edge_type": "SUPPORTS"})
	}
	verdict := func(want string, eligible bool, tensions int, why string) {
		t.Helper()
		r, err := s.StandingReportFor("claim")
		if err != nil {
			t.Fatal(err)
		}
		if r.Standing != want || r.Eligible != eligible || mustStanding(t, s, "claim") != want {
			t.Fatalf("%s: standing %q, eligible %v; want %q, %v", why, r.Standing, r.Eligible, want, eligible)
		}
		pairs, err := s.TensionsView()
		if err != nil || len(pairs) != tensions {
			t.Fatalf("%s: %d open tension(s) (%v); want %d", why, len(pairs), err, tensions)
		}
	}
	verdict("confirmed", true, 0, "three experiences")

	contradict := func(edge, from string) {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": edge, "from_id": from, "to_id": "claim", "edge_type": "CONTRADICTS"})
	}
	// "other" contradicts it, then is archived.
	contradict("c_other", "other")
	verdict("suspect", false, 1, "a current belief contradicts it")
	mat(ledger.EventBeliefArchive, 3, map[string]any{"id": "other", "reason": "it was wrong"})
	verdict("confirmed", true, 0, "the belief that contradicted it is archived")

	// "rival" contradicts it, then is superseded by "rival2". The plain
	// rule: a superseded source no longer contradicts. A restatement that
	// still disagrees contradicts only through its own edge (the case the identity
	// named on 2026-11-08: the successor carries no contradiction of the
	// old belief's until one is drawn).
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "rival", "statement": "rival", "ring": 3, "confidence": 0.5})
	contradict("c_rival", "rival")
	verdict("suspect", false, 1, "a second current belief contradicts it")
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "rival2", "statement": "rival, said again", "ring": 3, "confidence": 0.5})
	mat(ledger.EventBeliefSupersede, 3, map[string]any{"old_id": "rival", "new_id": "rival2", "reason": "said better"})
	verdict("confirmed", true, 0, "the contradicting belief is superseded and its successor draws no contradiction")
	contradict("c_rival2", "rival2")
	verdict("suspect", false, 1, "the successor draws its own contradiction")

	// A contradiction from an experience is live whatever is retired.
	mat(ledger.EventEdgeArchive, 3, map[string]any{"id": "c_rival2"})
	contradict("c_exp", "exp4")
	verdict("suspect", false, 1, "an experience contradicts it")
}

// A SUSPECT BELIEF IS TOLD APART FROM AN UNCONFIRMED ONE (a review and the identity,
// 2026-11-08, P2): three distinct origins and a live contradiction is
// suspect, not "short of origins". Through the event path: the footing says
// suspect and does not stand, promotion's report is not eligible, and the
// contradiction is an open tension.
func TestASuspectBeliefsFootingSaysSuspectNotUnconfirmed(t *testing.T) {
	s, mat := reportStore(t)
	for i, from := range []string{"exp", "exp2", "exp3"} {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "sup" + string(rune('a'+i)), "from_id": from, "to_id": "claim", "edge_type": "SUPPORTS"})
	}
	mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "c_other", "from_id": "other", "to_id": "claim", "edge_type": "CONTRADICTS"})
	footings, err := s.BeliefFootings(t.Context(), []string{"claim", "other"})
	if err != nil {
		t.Fatal(err)
	}
	if f := footings["claim"]; f.Stands || !f.Suspect {
		t.Fatalf("a three-origin belief under a live contradiction reads %+v; want suspect, not standing", f)
	}
	if f := footings["other"]; f.Stands || f.Suspect {
		t.Fatalf("an unconfirmed belief with no contradiction reads %+v; want neither", f)
	}
	r, err := s.StandingReportFor("claim")
	if err != nil || r.Eligible || r.DistinctOrigins < 3 {
		t.Fatalf("promotion's report: eligible %v on %d origin(s) (%v); want refused, with its three origins counted", r.Eligible, r.DistinctOrigins, err)
	}
	pairs, err := s.TensionsView()
	if err != nil || len(pairs) != 1 || pairs[0].RightID != "claim" {
		t.Fatalf("the contradiction is not an open tension: %+v %v", pairs, err)
	}
}

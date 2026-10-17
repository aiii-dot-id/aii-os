package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestABeliefIsConfirmedByItsOwnEvidence(t *testing.T) {
	s, mat := reportStore(t)
	support := func(edge, from, kind string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": edge, "from_id": from, "to_id": "claim", "edge_type": kind})
	}
	standing := func(want string, eligible bool, sources int, why string) {
		t.Helper()
		r, err := s.StandingReportFor("claim")
		if err != nil {
			t.Fatal(err)
		}
		if r.Standing != want || r.Eligible != eligible || r.DistinctSources != sources {
			t.Fatalf("%s: standing %q, eligible %v, from %d distinct source(s); want %q, %v, from %d", why, r.Standing, r.Eligible, r.DistinctSources, want, eligible, sources)
		}
		if got := mustStanding(t, s, "claim"); got != want {
			t.Fatalf("%s: the verdict alone says %q, the report %q", why, got, want)
		}
	}

	// One source linked three ways is one source: a count is of what the
	// belief rests on, never of the links made to it.
	support("a1", "exp", "SUPPORTS")
	support("a2", "exp", "REINFORCED_BY")
	support("a3", "exp", "DERIVED_FROM")
	standing("new", false, 1, "one experience linked three ways")

	support("b1", "exp2", "SUPPORTS")
	standing("new", false, 2, "two of the resident's own experiences")

	// A source that resolves to nothing is still no source.
	if _, err := s.db.Exec(`INSERT INTO edges(id,from_id,to_id,edge_type,created_seq) VALUES('ghost','nobody','claim','SUPPORTS',1)`); err != nil {
		t.Fatal(err)
	}
	standing("new", false, 2, "two experiences and a source that does not exist")

	// The third of the resident's own experiences confirms. Every source
	// in this store has provenance self, and no operator turn exists.
	var turns int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&turns); err != nil || turns != 0 {
		t.Fatalf("rig: this store must hold no turn at all: %d %v", turns, err)
	}
	support("c1", "exp3", "SUPPORTS")
	standing("confirmed", true, 3, "three of the resident's own experiences")

	// A live contradiction still makes it suspect, and its end returns it.
	mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "contra", "from_id": "exp4", "to_id": "claim", "edge_type": "CONTRADICTS"})
	standing("suspect", false, 3, "contradicted")
	mat(ledger.EventEdgeArchive, 3, map[string]any{"id": "contra"})
	standing("confirmed", true, 3, "the contradiction archived")
}

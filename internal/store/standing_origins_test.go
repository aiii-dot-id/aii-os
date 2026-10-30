package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestABeliefsOriginsAreWhatItsSourcesRestOn(t *testing.T) {
	s, mat := reportStore(t)
	support := func(edge, from, to string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": edge, "from_id": from, "to_id": to, "edge_type": "SUPPORTS"})
	}
	report := func(standing string, sources, origins int, why string) {
		t.Helper()
		r, err := s.StandingReportFor("claim")
		if err != nil {
			t.Fatal(err)
		}
		if r.Standing != standing || r.DistinctSources != sources || r.DistinctOrigins != origins {
			t.Fatalf("%s: standing %q from %d source(s) on %d origin(s); want %q, %d, %d", why, r.Standing, r.DistinctSources, r.DistinctOrigins, standing, sources, origins)
		}
	}

	// The dream reads the one experience and lands its note, then its
	// marker, as the facility does.
	mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "dream_of_exp", "content": "the observation, as the dream put it", "provenance": "dream"})
	var noted uint64
	if err := s.db.QueryRow(`SELECT created_seq FROM experiences WHERE id = 'dream_of_exp'`).Scan(&noted); err != nil {
		t.Fatal(err)
	}
	mat(ledger.EventDreamRun, 3, map[string]any{"inputs": []string{"exp"}, "outputs": []uint64{noted}})
	// A belief derived from the same experience.
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "from_exp", "statement": "what the observation suggests", "ring": 3, "confidence": 0.5})
	mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "d0", "from_id": "exp", "to_id": "from_exp", "edge_type": "DERIVED_FROM"})

	support("s0", "exp", "claim")
	support("s1", "dream_of_exp", "claim")
	report("new", 2, 0, "below three sources the origins are not read")
	support("s2", "from_exp", "claim")
	report("new", 3, 1, "one experience, its dream's note and a belief derived from it")
	if got := mustStanding(t, s, "claim"); got != "new" {
		t.Fatalf("ONE EVENT SAID THREE WAYS CONFIRMED A BELIEF: the verdict alone says %q", got)
	}

	// A second observation is a second origin, and a third a third: the
	// third origin confirms, and the verdict alone says the same.
	support("s3", "exp2", "claim")
	report("new", 4, 2, "a second observation")
	support("s4", "exp3", "claim")
	report("confirmed", 5, 3, "a third observation")
	if got := mustStanding(t, s, "claim"); got != "confirmed" {
		t.Fatalf("three origins did not confirm: the verdict alone says %q", got)
	}
}

// ONE LINK TO A CONFIRMED BELIEF IS ONE SOURCE. The gate asks three
// sources as well as three origins, so a belief derived from a single
// belief does not inherit that belief's three origins through one edge.
func TestOneLinkToAConfirmedBeliefIsOneSource(t *testing.T) {
	s, mat := reportStore(t)
	for i, from := range []string{"exp", "exp2", "exp3"} {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "o" + string(rune('0'+i)), "from_id": from, "to_id": "other", "edge_type": "SUPPORTS"})
	}
	if got := mustStanding(t, s, "other"); got != "confirmed" {
		t.Fatalf("fixture: three observations did not confirm: %q", got)
	}
	mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "c0", "from_id": "other", "to_id": "claim", "edge_type": "DERIVED_FROM"})
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.Standing != "new" || r.Eligible || r.DistinctSources != 1 {
		t.Fatalf("a belief that rests on one confirmed belief: standing %q, eligible %v, %d source(s); want new, false, 1", r.Standing, r.Eligible, r.DistinctSources)
	}
}

// WHAT HAS NO ORIGIN, AND WHAT IS ITS OWN. A belief that rests only on
// the belief being judged adds no origin: support that goes round is not
// evidence. A dream's note whose marker never landed names no dream, and
// is its own origin, as any note with no recorded origin is.
func TestOriginsOfARingAndOfANoteWhoseDreamLeftNoMarker(t *testing.T) {
	s, mat := reportStore(t)
	edge := func(id, from, to string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": "SUPPORTS"})
	}
	edge("r0", "claim", "other") // "other" rests on the claim alone
	edge("r1", "other", "claim")
	edge("r2", "exp", "claim")
	edge("r3", "exp2", "claim")
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != 3 || r.DistinctOrigins != 2 {
		t.Fatalf("a belief that rests only on the claim was counted as an origin: %d source(s), %d origin(s); want 3 and 2", r.DistinctSources, r.DistinctOrigins)
	}

	mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "orphan_note", "content": "a dream's note with no marker after it", "provenance": "dream"})
	edge("r4", "orphan_note", "claim")
	if r, err = s.StandingReportFor("claim"); err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != 4 || r.DistinctOrigins != 3 {
		t.Fatalf("a dream's note with no marker: %d source(s), %d origin(s); want 4 and 3", r.DistinctSources, r.DistinctOrigins)
	}

	// Another dream's marker after it does not adopt it: a marker speaks
	// for the outputs it names and for no other note.
	mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "later_note", "content": "a later dream's note", "provenance": "dream"})
	var later uint64
	if err := s.db.QueryRow(`SELECT created_seq FROM experiences WHERE id = 'later_note'`).Scan(&later); err != nil {
		t.Fatal(err)
	}
	mat(ledger.EventDreamRun, 3, map[string]any{"inputs": []string{"exp"}, "outputs": []uint64{later}})
	if r, err = s.StandingReportFor("claim"); err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != 4 || r.DistinctOrigins != 3 {
		t.Fatalf("a later dream's marker adopted a note it does not name: %d source(s), %d origin(s); want 4 and 3", r.DistinctSources, r.DistinctOrigins)
	}
}

// THE CENSUS COUNTS AND NAMES NOTHING: how many live beliefs hold three
// sources, and on how many origins those rest, by ring.
func TestTheOriginCensusCounts(t *testing.T) {
	s, mat := reportStore(t)
	edge := func(id, from, to, kind string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": kind})
	}
	// claim: three sources on one origin.
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "from_exp", "statement": "derived", "ring": 3, "confidence": 0.5})
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "from_exp_too", "statement": "derived again", "ring": 3, "confidence": 0.5})
	edge("d0", "exp", "from_exp", "DERIVED_FROM")
	edge("d1", "exp", "from_exp_too", "DERIVED_FROM")
	edge("c0", "exp", "claim", "SUPPORTS")
	edge("c1", "from_exp", "claim", "SUPPORTS")
	edge("c2", "from_exp_too", "claim", "SUPPORTS")
	// other: four sources on four origins, counted with "three or more".
	edge("o0", "exp2", "other", "SUPPORTS")
	edge("o1", "exp3", "other", "SUPPORTS")
	edge("o2", "exp4", "other", "SUPPORTS")
	edge("o3", "exp", "other", "SUPPORTS")

	// A belief that is archived is not counted at all.
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "retired", "statement": "no longer held", "ring": 3, "confidence": 0.5})
	if _, err := s.db.Exec(`UPDATE beliefs SET archived = 1 WHERE id = 'retired'`); err != nil {
		t.Fatal(err)
	}

	c, err := s.BeliefOriginCensus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := OriginCensus{Beliefs: 4, Confirmable: 2, ByOrigins: [4]int{0, 1, 0, 1}}
	if c != want {
		t.Fatalf("census %+v, want %+v", c, want)
	}

	// The same store with two of them at Ring 2: one on three origins, one
	// that holds a single source.
	if _, err := s.db.Exec(`UPDATE beliefs SET ring = 2 WHERE id IN ('other', 'from_exp')`); err != nil {
		t.Fatal(err)
	}
	c, err = s.BeliefOriginCensus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want = OriginCensus{Beliefs: 4, Ring2: 2, Confirmable: 2, ByOrigins: [4]int{0, 1, 0, 1}, Ring2ByOrigins: [4]int{0, 0, 0, 1}, Ring2UnderThreeSources: 1}
	if c != want {
		t.Fatalf("census with Ring 2: %+v, want %+v", c, want)
	}
}

// WHAT THE WALK DID NOT OPEN IS NOT AN ORIGIN. The walk of origins opens a
// bounded number of sources. A source it met after that used to be counted
// as an origin of its own, "erring toward more origins"; more origins is
// what confirms a belief, so a belief that rested on ONE observation,
// reached through a long enough tangle of restatements, was confirmed by
// the size of the tangle.
//
// Here the claim has three sources. One is the last of a chain of beliefs,
// each derived from the one before, longer than the walk's bound, that
// begins at a single observation; the other two are beliefs derived from
// that same observation. There is one origin. The walk meets its bound
// inside the chain: it must say that it left sources unopened and count
// none of them, and the claim must not stand confirmed. Then the claim is
// given three observations of its own. The walk opens what a claim rests
// on directly before what lies behind it, so it finds those three however
// long the chain beside them, and the claim is confirmed by them; the
// report still says the walk left sources unopened.
func TestASourceTheOriginsWalkDidNotOpenIsNotCountedAsAnOrigin(t *testing.T) {
	s, mat := reportStore(t)
	belief := func(id string) {
		t.Helper()
		mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": id, "statement": "a restatement, " + id, "ring": 3, "confidence": 0.5})
	}
	edge := func(id, from, to, kind string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": kind})
	}
	// chain_000 is derived from the observation; each link from the one
	// before; the claim rests on the last.
	const links = standingOriginBound + 40
	prev := "exp"
	for i := 0; i < links; i++ {
		id := fmt.Sprintf("chain_%03d", i)
		belief(id)
		edge("ce_"+id, prev, id, "DERIVED_FROM")
		prev = id
	}
	edge("s_chain", prev, "claim", "SUPPORTS")
	for _, id := range []string{"said_again", "said_once_more"} {
		belief(id)
		edge("de_"+id, "exp", id, "DERIVED_FROM")
		edge("s_"+id, id, "claim", "SUPPORTS")
	}
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != 3 {
		t.Fatalf("rig: the claim has three sources, read %d", r.DistinctSources)
	}
	if r.Standing == "confirmed" || r.Eligible {
		t.Fatalf("ONE OBSERVATION BEHIND A LONG CHAIN CONFIRMED A BELIEF: standing %q, eligible %v, %d origin(s) counted, %d source(s) unopened", r.Standing, r.Eligible, r.DistinctOrigins, r.OriginsUnopened)
	}
	if r.OriginsUnopened == 0 || r.DistinctOrigins >= 3 {
		t.Fatalf("the walk met its bound inside the chain: it must report sources unopened and count none of them, got %d origin(s) and %d unopened", r.DistinctOrigins, r.OriginsUnopened)
	}
	if got := mustStanding(t, s, "claim"); got == "confirmed" {
		t.Fatalf("the verdict alone confirms it: %q", got)
	}

	// Three observations of the claim's own. The chain's last link sorts
	// before them, and a walk that went to the end of one source before it
	// opened the next would spend its whole bound there.
	for i, from := range []string{"exp", "exp2", "exp3"} {
		edge(fmt.Sprintf("a_own_%d", i), from, "claim", "SUPPORTS")
	}
	if r, err = s.StandingReportFor("claim"); err != nil {
		t.Fatal(err)
	}
	if r.Standing != "confirmed" || r.DistinctOrigins != 3 || r.OriginsUnopened == 0 {
		t.Fatalf("three observations found before the bound: standing %q on %d origin(s), %d unopened; want confirmed on 3, with the chain's sources unopened", r.Standing, r.DistinctOrigins, r.OriginsUnopened)
	}
}

// A RING THAT DOES NOT PASS THROUGH THE CLAIM IS WALKED ONCE. Two beliefs
// that rest on each other have no origin, and the walk must come to its
// end there: it opens each source once, finds the one observation beside
// them, and leaves nothing unopened. A walk that opened a source again
// each time it met it would go round the ring until its bound.
func TestTheOriginsWalkOpensEachSourceOfARingOnce(t *testing.T) {
	s, mat := reportStore(t)
	for _, id := range []string{"ring_a", "ring_b"} {
		mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": id, "statement": "one of two beliefs that rest on each other, " + id, "ring": 3, "confidence": 0.5})
	}
	for i, e := range [][2]string{{"ring_a", "ring_b"}, {"ring_b", "ring_a"}, {"ring_a", "claim"}, {"ring_b", "claim"}, {"exp", "claim"}} {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": fmt.Sprintf("g%d", i), "from_id": e[0], "to_id": e[1], "edge_type": "SUPPORTS"})
	}
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != 3 || r.DistinctOrigins != 1 || r.OriginsUnopened != 0 {
		t.Fatalf("two beliefs in a ring and one observation: %d source(s), %d origin(s), %d unopened; want 3, 1 and 0", r.DistinctSources, r.DistinctOrigins, r.OriginsUnopened)
	}
}

func TestADreamsNoteIsOneOrigin(t *testing.T) {
	s, mat := reportStore(t)
	edge := func(id, from, to, kind string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": kind})
	}
	report := func(standing string, sources, origins int, why string) {
		t.Helper()
		r, err := s.StandingReportFor("claim")
		if err != nil {
			t.Fatal(err)
		}
		if r.Standing != standing || r.DistinctSources != sources || r.DistinctOrigins != origins {
			t.Fatalf("%s: standing %q from %d source(s) on %d origin(s); want %q, %d, %d", why, r.Standing, r.DistinctSources, r.DistinctOrigins, standing, sources, origins)
		}
		if got := mustStanding(t, s, "claim"); got != standing {
			t.Fatalf("%s: the verdict alone says %q; want %q", why, got, standing)
		}
	}

	// A dream reads twenty experiences and lands one note, then its marker.
	var read []string
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("seen%02d", i)
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": fmt.Sprintf("observation %d", i), "provenance": "self"})
		read = append(read, id)
	}
	mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "note", "content": "what the dream noticed across them", "provenance": "dream"})
	var noted uint64
	if err := s.db.QueryRow(`SELECT created_seq FROM experiences WHERE id = 'note'`).Scan(&noted); err != nil {
		t.Fatal(err)
	}
	mat(ledger.EventDreamRun, 3, map[string]any{"inputs": read, "outputs": []uint64{noted}})
	// Two beliefs drawn from the note.
	for _, b := range []string{"drawn1", "drawn2"} {
		mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": b, "statement": b, "ring": 3, "confidence": 0.5})
		edge("d_"+b, "note", b, "DERIVED_FROM")
	}
	edge("s0", "note", "claim", "SUPPORTS")
	edge("s1", "drawn1", "claim", "SUPPORTS")
	edge("s2", "drawn2", "claim", "SUPPORTS")
	report("new", 3, 1, "a dream's note of twenty experiences and two beliefs drawn from it")

	// Three of the experiences its dream read, beside it, are three
	// origins, and the note adds none to them.
	edge("s3", "seen00", "claim", "SUPPORTS")
	edge("s4", "seen01", "claim", "SUPPORTS")
	report("new", 5, 2, "the note beside two experiences its dream read")
	edge("s5", "seen02", "claim", "SUPPORTS")
	report("confirmed", 6, 3, "the note beside three experiences its dream read")
}

func TestTheOriginsOfDreamNotesAreTheSameInEveryOrderOfTheirIDs(t *testing.T) {
	// A shape: what each note's dream read (0..exps-1 an experience, exps+j
	// the j-th note, read only by a later one), and which experiences and
	// notes support the claim.
	type shape struct {
		exps          int
		reads         [][]int
		byExp, byNote []int
		want          int // the count, where the case is named; 0 where not
	}
	shapes := []shape{
		{exps: 2, reads: [][]int{{0}, {0, 1}, {1}}, byNote: []int{0, 1, 2}, want: 1},
		// A note whose dream read an earlier note says again what that
		// note's dream read.
		{exps: 2, reads: [][]int{{0}, {2}}, byExp: []int{0, 1}, byNote: []int{1}, want: 2},
	}
	rng := rand.New(rand.NewPCG(5, 175))
	for len(shapes) < 42 {
		sh := shape{exps: 4}
		for j := 0; j < 3; j++ {
			var read []int
			for len(read) == 0 {
				for i := 0; i < sh.exps+j; i++ {
					if rng.IntN(3) == 0 {
						read = append(read, i)
					}
				}
			}
			sh.reads = append(sh.reads, read)
		}
		for i := 0; i < sh.exps; i++ {
			if rng.IntN(3) == 0 {
				sh.byExp = append(sh.byExp, i)
			}
		}
		for j := range sh.reads {
			if rng.IntN(4) != 0 {
				sh.byNote = append(sh.byNote, j)
			}
		}
		if len(sh.byExp)+len(sh.byNote) >= 3 {
			shapes = append(shapes, sh)
		}
	}
	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}

	for n, sh := range shapes {
		// Before: each note is what its dream read, opened in turn.
		var expand func(i int) []int
		expand = func(i int) []int {
			if i < sh.exps {
				return []int{i}
			}
			var out []int
			for _, r := range sh.reads[i-sh.exps] {
				out = append(out, expand(r)...)
			}
			return out
		}
		before := map[int]bool{}
		for _, i := range sh.byExp {
			before[i] = true
		}
		for _, j := range sh.byNote {
			for _, i := range expand(sh.exps + j) {
				before[i] = true
			}
		}

		s, mat := reportStore(t)
		counts := map[int][][]int{}
		for o, order := range orders {
			name := func(i int) string {
				if i < sh.exps {
					return fmt.Sprintf("o%d-x%d", o, i)
				}
				return fmt.Sprintf("o%d-n%c", o, 'a'+order[i-sh.exps])
			}
			for i := 0; i < sh.exps; i++ {
				mat(ledger.EventExperienceCreate, 3, map[string]any{"id": name(i), "content": name(i), "provenance": "self"})
			}
			for j, read := range sh.reads {
				id := name(sh.exps + j)
				mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": id, "provenance": "dream"})
				var seq uint64
				if err := s.db.QueryRow(`SELECT created_seq FROM experiences WHERE id = ?`, id).Scan(&seq); err != nil {
					t.Fatal(err)
				}
				var inputs []string
				for _, i := range read {
					inputs = append(inputs, name(i))
				}
				mat(ledger.EventDreamRun, 3, map[string]any{"inputs": inputs, "outputs": []uint64{seq}})
			}
			claim := fmt.Sprintf("o%d-claim", o)
			mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": claim, "statement": claim, "ring": 3, "confidence": 0.5})
			support := func(i int) {
				mat(ledger.EventEdgeCreate, 3, map[string]any{"id": claim + "-" + name(i), "from_id": name(i), "to_id": claim, "edge_type": "SUPPORTS"})
			}
			for _, i := range sh.byExp {
				support(i)
			}
			for _, j := range sh.byNote {
				support(sh.exps + j)
			}
			r, err := s.StandingReportFor(claim)
			if err != nil {
				t.Fatal(err)
			}
			counts[r.DistinctOrigins] = append(counts[r.DistinctOrigins], order)
		}
		if len(counts) != 1 {
			t.Fatalf("shape %d %+v: the count differs with the order of the notes' ids (count: orders) %v", n, sh, counts)
		}
		for got := range counts {
			if got > len(before) || got < 1 {
				t.Fatalf("shape %d %+v: %d origin(s); want 1 to %d (the count before a note was one origin)", n, sh, got, len(before))
			}
			if sh.want != 0 && got != sh.want {
				t.Fatalf("shape %d %+v: %d origin(s); want %d", n, sh, got, sh.want)
			}
		}
	}
}

// AN UNOPENED INPUT KEEPS THE COUNT A FLOOR. Two notes whose dreams each
// read an earlier note, both of which read one observation, are one group
// and one origin. When the walk's bound leaves those earlier notes
// unopened, it cannot see that the two groups meet; it takes every group
// an unopened input touches as one, so the count is still at most what
// the record holds, and it says what it left unopened.
func TestAnInputTheOriginsWalkDidNotOpenJoinsTheGroupsItTouches(t *testing.T) {
	s, mat := reportStore(t)
	note := func(id string, read ...string) {
		t.Helper()
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": id, "provenance": "dream"})
		var seq uint64
		if err := s.db.QueryRow(`SELECT created_seq FROM experiences WHERE id = ?`, id).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		mat(ledger.EventDreamRun, 3, map[string]any{"inputs": read, "outputs": []uint64{seq}})
	}
	support := func(from string) {
		t.Helper()
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "s_" + from, "from_id": from, "to_id": "claim", "edge_type": "SUPPORTS"})
	}
	note("inner_a", "exp")
	note("inner_b", "exp")
	note("note_a", "inner_a")
	note("note_b", "inner_b")
	support("note_a")
	support("note_b")
	// The claim's other sources spend the rest of the walk's bound.
	for i := 0; i < standingOriginBound-2; i++ {
		id := fmt.Sprintf("obs_%03d", i)
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": id, "provenance": "self"})
		support(id)
	}
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.DistinctSources != standingOriginBound || r.OriginsUnopened != 2 {
		t.Fatalf("rig: %d source(s), %d unopened; want %d and the two earlier notes", r.DistinctSources, r.OriginsUnopened, standingOriginBound)
	}
	if want := standingOriginBound - 2 + 1; r.DistinctOrigins != want {
		t.Fatalf("two notes whose unopened inputs may meet: %d origin(s); want %d (the observations, and the two notes as one)", r.DistinctOrigins, want)
	}
}

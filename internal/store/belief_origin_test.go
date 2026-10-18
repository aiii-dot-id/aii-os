package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func origin(id string) []map[string]string {
	return []map[string]string{{"kind": "experience", "id": id}}
}

func TestBeliefOriginIsOneReplayableEvent(t *testing.T) {
	s := testStore(t)
	source := seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{
		"id": "exp_source", "content": "observed", "provenance": "self",
	})
	payload := map[string]any{"id": "b_claim", "statement": "A claim with a source", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, payload)); err != nil {
		t.Fatalf("admit a sourced belief: %v", err)
	}
	beliefEvent := seqEvent(t, s, 2, ledger.EventBeliefUpsert, payload)
	for _, read := range []*Store{s, testStore(t)} {
		if read != s {
			if err := read.ReplayAll(EventSlice([]ledger.Event{*source, *beliefEvent})); err != nil {
				t.Fatal(err)
			}
		}
		belief, err := read.GetBelief("b_claim")
		if err != nil || belief.EvidenceCount != 1 {
			t.Fatalf("belief lacks its origin after materialization/replay: %+v %v", belief, err)
		}
		edges, err := read.ListEdgesForBelief("b_claim")
		if err != nil || len(edges) != 1 || edges[0].FromID != "exp_source" || edges[0].ToID != "b_claim" || edges[0].EdgeType != "DERIVED_FROM" {
			t.Fatalf("origin edge did not replay: %+v %v", edges, err)
		}
	}
}

func TestBeliefOriginFailureLeavesNoBelief(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "shared", "content": "observed"})
	seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{"id": "shared", "statement": "historical collision", "ring": 3, "confidence": 0.5})
	cases := []struct {
		name string
		refs []map[string]string
	}{
		{"none", nil},
		{"ghost", origin("missing")},
		{"wrong kind", []map[string]string{{"kind": "intention", "id": "shared"}}},
		{"self", []map[string]string{{"kind": "belief", "id": "b_candidate"}}},
		{"ambiguous", origin("shared")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"id": "b_candidate", "statement": "a different claim", "ring": 3, "confidence": 0.5, "origin_refs": tc.refs}
			if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, payload)); err == nil {
				t.Fatal("invalid origin was admitted")
			}
			if _, err := s.GetBelief("b_candidate"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("refusal left a belief: %v", err)
			}
		})
	}
}

// A source is resolved by its id, so an id held by a belief and an experience
// resolves to neither and no belief resting on it has a standing again. The
// resident chooses a new belief's id; the door refuses the mint that would
// make the second holder, in either direction.
func TestAnIDOneKindOfSourceHoldsIsNotMintedAsTheOther(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "exp_note", "content": "the tide came in at noon"})
	seqEvent(t, s, 2, ledger.EventExperienceCreate, map[string]any{"id": "exp_other", "content": "the bread rose slowly"})
	seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{"id": "b_rests", "statement": "Tides follow the moon", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_note")})
	for _, typ := range []ledger.EventType{ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert} {
		payload := map[string]any{"id": "exp_note", "statement": "Bread needs salt", "content": "Bread needs salt", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_other")}
		if err := s.ValidateEvent(typ, 3, mustPayload(t, payload)); !errors.Is(err, ErrSourceIDHeld) || !strings.Contains(err.Error(), `"exp_note"`) {
			t.Fatalf("%s was admitted under an experience's id: %v", typ, err)
		}
	}
	note := map[string]any{"id": "b_rests", "content": "a note under a belief's id"}
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 3, mustPayload(t, note)); !errors.Is(err, ErrSourceIDHeld) || !strings.Contains(err.Error(), `"b_rests"`) {
		t.Fatalf("experience.create was admitted under a belief's id: %v", err)
	}
	for id, table := range map[string]string{"exp_note": "beliefs", "b_rests": "experiences"} {
		var held int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&held); err != nil || held != 0 {
			t.Fatalf("a refused mint left %q in %s: %d %v", id, table, held, err)
		}
	}
	// What the refusal keeps: the belief resting on the note has a standing.
	if standing, err := s.StandingFor("b_rests"); err != nil || standing != "new" {
		t.Fatalf("the belief resting on the note lost its standing: %q %v", standing, err)
	}
	// A record that already holds such a pair still materializes: the rule
	// is the live door's, never replay's.
	seqEvent(t, s, 4, ledger.EventBeliefUpsert, map[string]any{"id": "exp_note", "statement": "recorded before the rule", "ring": 3, "confidence": 0.5})
	// ONLY THE MINT THAT MAKES THE SECOND HOLDER IS REFUSED. The pair the
	// record now holds is still written under its id, in either table: its
	// words under another id would be a duplicate, so refusing this would
	// close the one id that belief has.
	again := map[string]any{"id": "exp_note", "statement": "recorded before the rule", "ring": 3, "confidence": 0.6, "origin_refs": origin("exp_other")}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, again)); errors.Is(err, ErrSourceIDHeld) {
		t.Fatalf("AN UPDATE OF A BELIEF THE RECORD ALREADY HOLDS WAS REFUSED AS A NEW MINT: %v", err)
	}
}

func TestEveryClaimedOriginMustBecomeAnActiveEdge(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "e1", "content": "one"})
	seqEvent(t, s, 2, ledger.EventExperienceCreate, map[string]any{"id": "e2", "content": "two"})
	seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{"id": "b", "statement": "claim", "ring": 3, "confidence": 0.5})
	seqEvent(t, s, 4, ledger.EventEdgeCreate, map[string]any{"id": "edge_one", "from_id": "e1", "to_id": "b", "edge_type": "DERIVED_FROM"})
	seqEvent(t, s, 5, ledger.EventEdgeCreate, map[string]any{"id": "edge_two", "from_id": "e2", "to_id": "b", "edge_type": "DERIVED_FROM"})
	seqEvent(t, s, 6, ledger.EventEdgeArchive, map[string]any{"id": "edge_two"})
	payload := map[string]any{"id": "b", "statement": "claim", "ring": 3, "confidence": 0.5,
		"origin_refs": []map[string]string{{"kind": "experience", "id": "e1"}, {"kind": "experience", "id": "e2"}}}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, payload)); err == nil || !strings.Contains(err.Error(), "omit active origin") {
		t.Fatalf("an archived relation silently satisfied an origin: %v", err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&rows); err != nil || rows != 6 {
		t.Fatalf("rejected origin changed the record mirror: rows=%d err=%v", rows, err)
	}
}

func TestBeliefDuplicateAdmissionCannotBeOverridden(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "exp_source", "content": "observed"})
	seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{"id": "b_first", "statement": "The ledger is immutable", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")})
	for _, typ := range []ledger.EventType{ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert} {
		payload := map[string]any{"id": "b_second", "statement": "  the LEDGER  is immutable ", "content": "  the LEDGER  is immutable ", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")}
		if err := s.ValidateEvent(typ, 3, mustPayload(t, payload)); err == nil || !strings.Contains(err.Error(), "already states this") {
			t.Fatalf("%s admitted an exact duplicate: %v", typ, err)
		}
		payload["duplicate_ok"] = true
		if err := s.ValidateEvent(typ, 3, mustPayload(t, payload)); err == nil {
			t.Fatalf("%s accepted a belief duplicate override", typ)
		}
	}
	// The shared door must compare working_style.content, the value the
	// materializer writes, even when statement claims unrelated words.
	conflicting := map[string]any{"id": "ws_second", "statement": "unrelated words",
		"content": "The ledger is immutable", "origin_refs": origin("exp_source"),
		"distinct_from": []string{"b_first"}, "distinction": "claimed difference"}
	if err := s.ValidateEvent(ledger.EventWorkingStyleUpsert, 3, mustPayload(t, conflicting)); err == nil || !strings.Contains(err.Error(), "already states this") {
		t.Fatalf("working style compared the wrong text and admitted a duplicate: %v", err)
	}
	near := map[string]any{"id": "b_second", "statement": "The ledger is not immutable", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, near)); err == nil || !strings.Contains(err.Error(), "may already express") {
		t.Fatalf("near claim did not require a distinction: %v", err)
	}
	near["distinct_from"] = []string{"b_first"}
	near["distinction"] = "This claim negates the earlier one."
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, near)); err != nil {
		t.Fatalf("a stated distinction was refused: %v", err)
	}
	near["distinct_from"] = []string{"b_first", "b_missing"}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, near)); err == nil || !strings.Contains(err.Error(), "does not name an active belief") {
		t.Fatalf("a fabricated distinction target was accepted: %v", err)
	}
	// An identity may deliberately distinguish a semantically related belief
	// whose wording falls below the lexical candidate threshold.
	seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{"id": "b_far", "statement": "Our decisions need audit receipts", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")})
	near["distinct_from"] = []string{"b_first", "b_far"}
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, near)); err != nil {
		t.Fatalf("an explicit distinction from another active belief was refused: %v", err)
	}
}

// The door's same-id rule read active beliefs only, and the materializer
// finds a row by id whatever its state: a promotion of a retired belief, and
// an upsert over it, in other words or its own, were admitted, changed the
// retired row and made nothing live. The retirement is history: each is
// refused, and the claim stated again is a new belief under a new id.
func TestARetiredBeliefIsNeitherPromotedNorWrittenOverAtTheDoor(t *testing.T) {
	for _, retired := range []struct {
		name    string
		typ     ledger.EventType
		payload map[string]any
		how     string
	}{
		{"archived", ledger.EventBeliefArchive, map[string]any{"id": "b_gone", "reason": "no longer held"}, "which is archived"},
		{"superseded", ledger.EventBeliefSupersede, map[string]any{"old_id": "b_gone", "new_id": "b_next", "reason": "sharper"}, `superseded by "b_next"`},
	} {
		t.Run(retired.name, func(t *testing.T) {
			s := testStore(t)
			seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "exp_source", "content": "observed"})
			seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{"id": "b_gone", "statement": "Tides follow the moon", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")})
			seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{"id": "b_next", "statement": "Bread needs salt", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")})
			seqEvent(t, s, 4, retired.typ, retired.payload)

			promote := map[string]any{"id": "b_gone", "ring": 2}
			err := s.ValidateEvent(ledger.EventBeliefPromote, 2, mustPayload(t, promote))
			if !errors.Is(err, ErrBeliefRetired) || !strings.Contains(err.Error(), retired.how) {
				t.Fatalf("a retired belief was promoted: %v", err)
			}
			// A producer sends the model's own arguments: a field of the
			// wrong type beside the id must not carry a promotion past.
			stray := map[string]any{"id": "b_gone", "ring": 2, "statement": 5, "content": []int{1}}
			if err := s.ValidateEvent(ledger.EventBeliefPromote, 2, mustPayload(t, stray)); !errors.Is(err, ErrBeliefRetired) {
				t.Fatalf("A PROMOTION WITH A STRAY FIELD PASSED THE RETIRED RULE: %v", err)
			}
			// The claim's own words, stated again under the retired id.
			same := map[string]any{"id": "b_gone", "statement": "Tides follow the moon", "content": "Tides follow the moon", "ring": 3, "confidence": 0.9, "origin_refs": origin("exp_source")}
			for _, typ := range []ledger.EventType{ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert} {
				if err := s.ValidateEvent(typ, 3, mustPayload(t, same)); !errors.Is(err, ErrBeliefRetired) || !strings.Contains(err.Error(), "under a new id") {
					t.Fatalf("%s landed a claim stated again on its retired row: %v", typ, err)
				}
			}
			// Stated again under a new id, it is a new belief.
			same["id"] = "b_tides_again"
			if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, same)); err != nil {
				t.Fatalf("a retired claim stated again under a new id was refused: %v", err)
			}
			reworded := map[string]any{"id": "b_gone", "statement": "Rivers run to the sea", "content": "Rivers run to the sea", "ring": 3, "confidence": 0.9, "origin_refs": origin("exp_source")}
			for _, typ := range []ledger.EventType{ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert} {
				err := s.ValidateEvent(typ, 3, mustPayload(t, reworded))
				if !errors.Is(err, ErrBeliefRetired) || !strings.Contains(err.Error(), "under a new id") {
					t.Fatalf("%s wrote other words over a retired belief: %v", typ, err)
				}
			}
			var statement string
			var ring int
			if err := s.db.QueryRow(`SELECT statement, ring FROM beliefs WHERE id = 'b_gone'`).Scan(&statement, &ring); err != nil || statement != "Tides follow the moon" || ring != 3 {
				t.Fatalf("a refused write changed the retired belief: %q ring %d %v", statement, ring, err)
			}
			// The new words under a new id are an ordinary new belief.
			reworded["id"] = "b_rivers"
			if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustPayload(t, reworded)); err != nil {
				t.Fatalf("the route the refusal names is closed: %v", err)
			}
			// A record that already holds such writes still materializes: the
			// rule is the live door's, never replay's.
			seqEvent(t, s, 5, ledger.EventBeliefPromote, promote)
			reworded["id"] = "b_gone"
			delete(reworded, "origin_refs") // replay of an older admitted event
			seqEvent(t, s, 6, ledger.EventBeliefUpsert, reworded)
		})
	}
}

// A belief is superseded by an active belief. The successor check read
// archived alone, so a belief already superseded passed as a successor and a
// live belief could be retired toward a row no reader lists.
func TestABeliefIsNotSupersededTowardASupersededBelief(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "exp_source", "content": "observed"})
	for i, b := range []struct{ id, statement string }{{"b_live", "Tides follow the moon"}, {"b_old", "Bread needs salt"}, {"b_new", "Rivers run to the sea"}} {
		seqEvent(t, s, uint64(2+i), ledger.EventBeliefUpsert, map[string]any{"id": b.id, "statement": b.statement, "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_source")})
	}
	seqEvent(t, s, 5, ledger.EventBeliefSupersede, map[string]any{"old_id": "b_old", "new_id": "b_new", "reason": "sharper"})
	toward := map[string]any{"old_id": "b_live", "new_id": "b_old", "reason": "toward a retired row"}
	if err := s.ValidateEvent(ledger.EventBeliefSupersede, 3, mustPayload(t, toward)); !errors.Is(err, ErrBeliefRetired) || !strings.Contains(err.Error(), `superseded by "b_new"`) {
		t.Fatalf("a live belief was superseded toward a superseded one: %v", err)
	}
	toward["new_id"] = "b_new"
	if err := s.ValidateEvent(ledger.EventBeliefSupersede, 3, mustPayload(t, toward)); err != nil {
		t.Fatalf("a supersede toward an active belief was refused: %v", err)
	}
}

func TestBeliefGroundingAuditUsesLiveIncomingEdges(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]any{"id": "b_source", "statement": "source", "ring": 3, "confidence": 0.5})
	seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{"id": "b_target", "statement": "target", "ring": 3, "confidence": 0.5})
	seqEvent(t, s, 3, ledger.EventEdgeCreate, map[string]any{"id": "ed_support", "from_id": "b_source", "to_id": "b_target", "edge_type": "SUPPORTS"})
	a, err := s.GraphAudit()
	if err != nil || a.Ungrounded != 1 || len(a.UngroundedSample) != 1 || a.UngroundedSample[0] != "b_source" {
		t.Fatalf("outgoing edge falsely grounded its source: %+v %v", a, err)
	}
	seqEvent(t, s, 4, ledger.EventEdgeCreate, map[string]any{"id": "ed_ghost", "from_id": "missing", "to_id": "b_source", "edge_type": "SUPPORTS"})
	a, err = s.GraphAudit()
	if err != nil || a.Ungrounded != 1 || a.Orphans != 1 {
		t.Fatalf("ghost evidence falsely grounded its target: %+v %v", a, err)
	}
	if err := s.ValidateEvent(ledger.EventEdgeArchive, 3, mustPayload(t, map[string]any{"id": "ed_support"})); err == nil {
		t.Fatal("last valid source edge was allowed to strand an active belief")
	}
	seqEvent(t, s, 5, ledger.EventEdgeArchive, map[string]any{"id": "ed_support"}) // historical replay still reads
	a, err = s.GraphAudit()
	if err != nil || a.Ungrounded != 2 {
		t.Fatalf("archived last edge still grounded a belief: %+v %v", a, err)
	}
	b, err := s.GetBelief("b_target")
	if err != nil || b.EvidenceCount != 0 {
		t.Fatalf("archived edge left a positive evidence count: %+v %v", b, err)
	}
}

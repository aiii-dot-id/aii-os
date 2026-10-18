package store

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// AN ID BOTH KINDS HOLD IS READ AS THE ONE RECORDED FIRST (row 9.1). Before
// 0.2.0 nothing checked the id a belief or an experience was minted under,
// so a record can hold both under one id. Such an id resolved to neither,
// and every belief resting on it read "standing cannot be proven", for good.
// It is now read as whichever was recorded first, and the standing report
// says so beside the source. The record is not changed: these are the rows
// it already holds, materialized as a replay would.
func TestAnIDBothKindsHoldIsReadAsTheOneRecordedFirst(t *testing.T) {
	for _, tc := range []struct {
		name, first, note string
	}{
		{"the experience first", "experience", "this id also names a belief recorded later (record 2); it is read as the experience recorded first (record 1)"},
		{"the belief first", "belief", "this id also names an experience recorded later (record 2); it is read as the belief recorded first (record 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			experience := func(seq uint64) {
				seqEvent(t, s, seq, ledger.EventExperienceCreate, map[string]any{"id": "pair", "content": "observed", "provenance": "self"})
			}
			belief := func(seq uint64) {
				seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{"id": "pair", "statement": "the pair's belief", "ring": 3, "confidence": 0.5})
			}
			if tc.first == "experience" {
				experience(1)
				belief(2)
			} else {
				belief(1)
				experience(2)
			}
			seqEvent(t, s, 3, ledger.EventExperienceCreate, map[string]any{"id": "exp_two", "content": "observed again", "provenance": "self"})
			seqEvent(t, s, 4, ledger.EventBeliefUpsert, map[string]any{"id": "b_rests", "statement": "a claim resting on the pair", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_two")})
			seqEvent(t, s, 5, ledger.EventEdgeCreate, map[string]any{"id": "edge_pair", "from_id": "pair", "to_id": "b_rests", "edge_type": "SUPPORTS"})

			if kind, err := s.EvidenceOriginKind("pair"); err != nil || kind != tc.first {
				t.Fatalf("AN ID BOTH KINDS HOLD DID NOT RESOLVE TO THE ONE RECORDED FIRST: %q, %v", kind, err)
			}
			if _, err := s.StandingFor("b_rests"); err != nil {
				t.Fatalf("A BELIEF RESTING ON AN ID BOTH KINDS HOLD HAS NO STANDING: %v", err)
			}
			r, err := s.StandingDetail(t.Context(), "b_rests", &StandingCursor{})
			if err != nil {
				t.Fatal(err)
			}
			var said string
			for _, q := range r.Qualifying {
				if q.Edge.FromID == "pair" {
					if q.SourceKind != tc.first {
						t.Fatalf("the report counts the pair as %q, want %q", q.SourceKind, tc.first)
					}
					said = q.Note
				}
			}
			if said != tc.note {
				t.Fatalf("the standing report does not say how the pair was read:\n got %q\nwant %q", said, tc.note)
			}
			for _, q := range r.Qualifying {
				if q.Edge.FromID != "pair" && strings.TrimSpace(q.Note) != "" {
					t.Fatalf("a source no other kind holds carries a note: %+v", q)
				}
			}
		})
	}
}

// A RE-CREATE DOES NOT TURN AN OLD PAIR ROUND. The experience's row carries
// the record that wrote it last (created_seq), and a second
// experience.create under the id replaces the row: read from there, an
// experience recorded first (record 1), then the belief (record 2), then
// the experience created again (record 6) was read as the belief recorded
// first. Which was recorded first is read from what no later record
// overwrites: the record's first experience.create under the id, and the
// belief's first_seq.
func TestARecreateDoesNotTurnAnOldPairRound(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "pair", "content": "observed", "provenance": "self"})
	seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{"id": "pair", "statement": "the pair's belief", "ring": 3, "confidence": 0.5})
	seqEvent(t, s, 3, ledger.EventExperienceCreate, map[string]any{"id": "exp_two", "content": "observed again", "provenance": "self"})
	seqEvent(t, s, 4, ledger.EventBeliefUpsert, map[string]any{"id": "b_rests", "statement": "a claim resting on the pair", "ring": 3, "confidence": 0.5, "origin_refs": origin("exp_two")})
	seqEvent(t, s, 5, ledger.EventEdgeCreate, map[string]any{"id": "edge_pair", "from_id": "pair", "to_id": "b_rests", "edge_type": "SUPPORTS"})
	if kind, err := s.EvidenceOriginKind("pair"); err != nil || kind != "experience" {
		t.Fatalf("rig: the pair reads as the experience recorded first: %q, %v", kind, err)
	}
	seqEvent(t, s, 6, ledger.EventExperienceCreate, map[string]any{"id": "pair", "content": "observed, written again", "provenance": "self"})
	seqEvent(t, s, 7, ledger.EventBeliefUpsert, map[string]any{"id": "pair", "statement": "the pair's belief, said again", "ring": 3, "confidence": 0.6})
	if kind, err := s.EvidenceOriginKind("pair"); err != nil || kind != "experience" {
		t.Fatalf("A RE-CREATE TURNED THE PAIR ROUND: it reads as %q (%v), and the experience was recorded first", kind, err)
	}
	r, err := s.StandingDetail(t.Context(), "b_rests", &StandingCursor{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "this id also names a belief recorded later (record 2); it is read as the experience recorded first (record 1)"
	for _, q := range r.Qualifying {
		if q.Edge.FromID == "pair" && q.Note != want {
			t.Fatalf("the report reads the pair by its last writes:\n got %q\nwant %q", q.Note, want)
		}
	}
}

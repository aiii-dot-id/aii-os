package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestEveryShapeThisTreeWritesIsAdmitted(t *testing.T) {
	entries, _ := readCorpusManifest(t)
	written := 0
	for _, e := range entries {
		if !e.written {
			continue
		}
		written++
		events := corpusEvents(t, e.file)
		last := events[len(events)-1]
		if err := ledger.AdmitPayloadMembers(last.Type, last.Payload); err != nil {
			t.Errorf("%s: A SHAPE THIS TREE WRITES IS REFUSED AT THE DOOR: %v", e.file, err)
		}
	}
	if written == 0 {
		t.Fatal("the corpus marks no shape as written")
	}
}

// THE DOOR REFUSES A MEMBER ITS TYPE DOES NOT DECLARE, in the operator's
// words (drafts), naming the type, the member and what the type declares;
// a name that matches a declared one only when case is ignored is refused
// as that, since the materializer would have read it as the member; a
// member inside an object the type declares is held too. Nothing of a
// refused payload is materialized, and the same payload written by an
// earlier release still replays: the materializers stay tolerant.
func TestTheDoorRefusesAMemberItsTypeDoesNotDeclare(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{"id": "exp_source", "content": "observed"})
	admitted := map[string]any{"id": "exp_new", "content": "an invented note", "category": "observation", "provenance": "self"}
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 3, mustPayload(t, admitted)); err != nil {
		t.Fatalf("a payload of declared members was refused: %v", err)
	}
	for name, c := range map[string]struct {
		typ     ledger.EventType
		payload map[string]any
		want    string
	}{
		"an undeclared member": {
			ledger.EventExperienceCreate,
			map[string]any{"id": "exp_new", "content": "an invented note", "mood": "bright"},
			`experience.create does not declare the member "mood", so nothing was recorded; its members are category, cites, content, dreamed_through, id, model_id, provenance, raw, source_turn, source_url, unmetabolized`,
		},
		"a member spelled in another case": {
			ledger.EventExperienceCreate,
			map[string]any{"id": "exp_new", "Content": "an invented note", "content": "x"},
			`experience.create does not declare the member "Content" (its member is "content", spelled exactly so), so nothing was recorded`,
		},
		"a member inside a declared object": {
			ledger.EventBeliefUpsert,
			map[string]any{"variant": "belief.upsert", "id": "b_new", "statement": "an invented claim", "ring": 3, "confidence": 0.5,
				"origin_refs": []map[string]string{{"kind": "experience", "id": "exp_source", "why": "x"}}},
			`belief.upsert does not declare the member "origin_refs[].why", so nothing was recorded; its members are id, kind`,
		},
		"the verb's own words on a type it does not write": {
			ledger.EventTrustEpochAccepted,
			map[string]any{"root": "platform", "trust_epoch": 2, "payload_sha256": "ab", "variant": "trust.epoch_accepted"},
			`trust.epoch_accepted does not declare the member "variant"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ring := 3
			if c.typ == ledger.EventTrustEpochAccepted {
				ring = 0
			}
			err := s.ValidateEvent(c.typ, ring, mustPayload(t, c.payload))
			var undeclared *ledger.UndeclaredMemberError
			if !errors.As(err, &undeclared) || !strings.HasPrefix(err.Error(), c.want) {
				t.Fatalf("A MEMBER ITS TYPE DOES NOT DECLARE WAS ADMITTED, or refused in other words:\n got %v\nwant %s", err, c.want)
			}
		})
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM experiences WHERE id = 'exp_new'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused payload left a row: %d %v", n, err)
	}
	// The same undeclared member in a record an earlier release wrote: it
	// replays as it always did.
	recorded := map[string]any{"id": "exp_old", "content": "an invented earlier note", "mood": "bright", "Category": "work"}
	if err := s.ReplayAll(EventSlice([]ledger.Event{
		{Seq: 1, Type: ledger.EventExperienceCreate, Ring: 3, Payload: mustPayload(t, recorded), Timestamp: "2026-01-01T00:00:01Z"},
	})); err != nil {
		t.Fatalf("A RECORDED PAYLOAD WITH AN UNDECLARED MEMBER NO LONGER READS: %v", err)
	}
}

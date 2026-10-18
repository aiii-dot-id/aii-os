package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestARecordedPortraitCitingFewerThanFourClassesReplaysAsWritten(t *testing.T) {
	engine, st, lg, kp, dir := setupEngine(t)
	refs := selfModelSources(t, st, lg, kp)
	mint := func(typ ledger.EventType, ring int, payload map[string]any) {
		t.Helper()
		evt, err := lg.Append(typ, ring, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	mint(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_gone", "statement": "Speed matters more than care", "ring": 3, "confidence": 0.4})
	mint(ledger.EventBeliefArchive, 3, map[string]any{"id": "b_gone", "reason": "outgrown"})

	// What an import signs: the record, appended as it was lived there and
	// held to no admission of this tree's.
	recorded := func(id, previous string, cites []map[string]any) {
		t.Helper()
		payload := map[string]any{
			"id": id, "synthesis_text": "I was " + id + ".", "continuity_thread": "I carry what I was.",
			"source_entity_refs": cites,
		}
		if previous != "" {
			payload["previous_synthesis_id"] = previous
		}
		if _, err := lg.Append(ledger.EventSelfModelSynthesize, 3, payload, kp); err != nil {
			t.Fatal(err)
		}
	}
	recorded("syn_two_classes", "", refs[:2])
	recorded("syn_three_classes", "syn_two_classes", refs[:3])
	recorded("syn_cites_archived", "syn_three_classes", []map[string]any{
		{"class": "beliefs", "id": "b_gone"}, {"class": "experiences", "id": "x_self"},
		{"class": "intentions", "id": "i_self"}, {"class": "relationships", "id": "rel_self"},
	})

	rebuilt, err := store.NewForTest(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if err := rebuilt.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatalf("A PORTRAIT THE RECORD HOLDS WAS JUDGED AGAIN AT REPLAY, and the store cannot be rebuilt from its own ledger: %v", err)
	}
	current, err := rebuilt.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "syn_cites_archived" {
		t.Fatalf("after the replay the current portrait is %+v (%v); want syn_cites_archived", current, err)
	}
	all, err := rebuilt.ListSelfModelSyntheses(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	cited := map[string]int{}
	for _, p := range all {
		cited[p.ID] = len(p.SourceEntityRefs)
	}
	if cited["syn_two_classes"] != 2 || cited["syn_three_classes"] != 3 || cited["syn_cites_archived"] != 4 {
		t.Fatalf("each portrait is materialized with what it cites, as written: %v", cited)
	}

	// A NEW portrait is held to both at the door, before anything is signed.
	ctx := context.Background()
	before := lg.LastSeq()
	narrow := selfModelArgs("syn_new_narrow", refs[:3])
	narrow["previous_synthesis_id"] = "syn_cites_archived"
	var refusal *store.SelfModelRefusal
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", narrow); !errors.As(err, &refusal) || refusal.Requirement != "source coverage" || lg.LastSeq() != before {
		t.Fatalf("a new portrait citing three classes must be refused at admission, unappended: %v (ledger %d to %d)", err, before, lg.LastSeq())
	}
	archived := selfModelArgs("syn_new_archived", []map[string]any{
		{"class": "beliefs", "id": "b_gone"}, {"class": "experiences", "id": "x_self"},
		{"class": "intentions", "id": "i_self"}, {"class": "relationships", "id": "rel_self"},
	})
	archived["previous_synthesis_id"] = "syn_cites_archived"
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", archived); !errors.As(err, &refusal) || refusal.Requirement != "consistent cited evidence" || lg.LastSeq() != before {
		t.Fatalf("a new portrait citing an archived belief must be refused at admission, unappended: %v (ledger %d to %d)", err, before, lg.LastSeq())
	}
}

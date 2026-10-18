package store

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestLivePreflightDoesNotRewriteEarnedRing2Text(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  ledger.EventType
		text string
	}{
		{"belief statement", ledger.EventBeliefUpsert, "statement"},
		{"belief content", ledger.EventBeliefUpsert, "content"},
		{"working style", ledger.EventWorkingStyleUpsert, "content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]any{
				"id": "b_earned", "ring": 3, "statement": "I value honesty",
			})
			seqEvent(t, s, 2, ledger.EventBeliefPromote, map[string]any{"id": "b_earned", "ring": 2})
			seqEvent(t, s, 3, ledger.EventExperienceCreate, map[string]any{"id": "exp_origin", "content": "observed evidence"})
			change := map[string]any{"id": "b_earned", "ring": 3, tc.text: "Deception is acceptable", "origin_refs": []map[string]string{{"kind": "experience", "id": "exp_origin"}}}
			err := s.ValidateEvent(tc.typ, 3, mustRaw(t, change))
			if !errors.Is(err, ErrRing2TextChange) {
				t.Fatalf("Ring 3 replaced Ring 2 words without refusal: %v", err)
			}
			belief, err := s.GetBelief("b_earned")
			if err != nil || belief.Ring != 2 || belief.Statement != "I value honesty" {
				t.Fatalf("failed preflight changed the earned belief: %+v %v", belief, err)
			}
			var rows int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&rows); err != nil || rows != 3 {
				t.Fatalf("failed preflight left a ledger mirror row: %d %v", rows, err)
			}
			// Exact same-text maintenance is not a new authorship claim.
			change[tc.text] = "I value honesty"
			if err := s.ValidateEvent(tc.typ, 3, mustRaw(t, change)); err != nil {
				t.Fatalf("same-text maintenance refused: %v", err)
			}
			// An existing signed history still replays under the materializer
			// that originally admitted it. Only live preflight changes.
			change[tc.text] = "historical amended words"
			delete(change, "origin_refs") // replay of an older admitted event
			seqEvent(t, s, 4, tc.typ, change)
			belief, err = s.GetBelief("b_earned")
			if err != nil || belief.Ring != 2 || belief.Statement != "historical amended words" {
				t.Fatalf("historical materialization became unreadable: %+v %v", belief, err)
			}
		})
	}
}

func TestRing3RevisionAndRing2SupersessionStayUsable(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_earned", "ring": 3, "statement": "old value",
	})
	seqEvent(t, s, 2, ledger.EventBeliefPromote, map[string]any{"id": "b_earned", "ring": 2})
	seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_new", "ring": 3, "statement": "new, still working belief",
	})
	seqEvent(t, s, 4, ledger.EventExperienceCreate, map[string]any{"id": "exp_origin", "content": "observed evidence"})
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, 3, mustRaw(t, map[string]any{
		"id": "b_new", "ring": 3, "statement": "revised working belief", "origin_refs": []map[string]string{{"kind": "experience", "id": "exp_origin"}},
	})); err == nil {
		t.Fatal("a Ring 3 belief changed its words under old evidence instead of superseding")
	}
	if err := s.ValidateEvent(ledger.EventWorkingStyleUpsert, 3, mustRaw(t, map[string]any{
		"id": "b_style", "content": "a new working style", "origin_refs": []map[string]string{{"kind": "experience", "id": "exp_origin"}},
	})); err != nil {
		t.Fatalf("new working style refused: %v", err)
	}
	if err := s.ValidateEvent(ledger.EventBeliefSupersede, 3, mustRaw(t, map[string]any{
		"old_id": "b_earned", "new_id": "b_new", "reason": "my view changed",
	})); err != nil {
		t.Fatalf("resident could not supersede the earned belief: %v", err)
	}
}

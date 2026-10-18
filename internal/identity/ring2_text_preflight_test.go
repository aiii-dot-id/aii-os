package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestCommitCannotReplaceEarnedRing2WordsAndCanSupersede(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	commit := func(args map[string]any) error {
		t.Helper()
		_, err := engine.ExecuteAction(context.Background(), "verb", "commit", args)
		return err
	}
	if err := commit(map[string]any{
		"variant": "belief.upsert", "id": "b_earned", "statement": "I value honesty", "evidence_refs": "exp_test_origin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordConversationTurn("operator", "yes, that held up"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn unavailable: %+v %v", opTurn, err)
	}
	for _, note := range []map[string]any{
		{"content": "I watched it hold", "supports": "b_earned"},
		{"content": "the operator confirmed it", "source_turn": opTurn.TurnSeq, "supports": "b_earned"},
		{"content": "a second experience showed it", "supports": "b_earned"},
	} {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "note", note); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustStanding(t, st, "b_earned"); got != "confirmed" {
		t.Fatalf("belief was not earned: %s", got)
	}
	if err := commit(map[string]any{
		"variant": "belief.promote", "id": "b_earned", "ring": 2,
	}); err != nil {
		t.Fatal(err)
	}
	before := lg.LastSeq()
	for _, args := range []map[string]any{
		{"variant": "belief.upsert", "id": "b_earned", "statement": "Deception is acceptable", "evidence_refs": "exp_test_origin"},
		{"variant": "working_style.upsert", "id": "b_earned", "content": "Deception is acceptable", "evidence_refs": "exp_test_origin"},
	} {
		if err := commit(args); !errors.Is(err, store.ErrRing2TextChange) {
			t.Fatalf("Ring 2 rewrite did not receive the typed refusal: %v", err)
		}
	}
	if lg.LastSeq() != before {
		t.Fatalf("refused rewrite appended a record: %d -> %d", before, lg.LastSeq())
	}
	belief, err := st.GetBelief("b_earned")
	if err != nil || belief.Statement != "I value honesty" || belief.Ring != 2 {
		t.Fatalf("earned identity changed: %+v %v", belief, err)
	}
	if err := commit(map[string]any{
		"variant": "belief.upsert", "id": "b_new", "statement": "My conclusion changed", "evidence_refs": "exp_test_origin",
	}); err != nil {
		t.Fatalf("refusal froze an ordinary new belief: %v", err)
	}
	if err := commit(map[string]any{
		"variant": "belief.supersede", "old_id": "b_earned", "new_id": "b_new", "reason": "new evidence",
	}); err != nil {
		t.Fatalf("resident could not supersede the old belief: %v", err)
	}
}

package cognitive

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

// A PRESSED PASS READS A NAMED VOICE ONCE. A named voice's framed row is one unit in the
// intake: taken whole or not at all, its cursor at the row's recorded end, with no "the
// rest comes later" after it. Two passes over it and a plain turn after it, fitted as a
// model's input limit fits them: no word lost, repeated, or read in its raw frame.
func TestAPressedPassReadsANamedVoiceOnce(t *testing.T) {
	const named = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	s, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seq, err := s.AddConversationTurnSeq("participant", store.HeardFromSomeone("Same label", "the harbour at dawn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnnotateTurn(seq, store.AnnotationSpeaker, "vs-1/1", `{"speaker_uuid":"`+named+`","continuity":"matched","registry_revision":"9"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddConversationTurnSeq("operator", "a plain turn after it"); err != nil {
		t.Fatal(err)
	}
	s.SetOperatorVoices([]string{named})

	// The first pass: the limit admits the first part, and no more.
	first, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	kept := first.Keep(1, 0)
	heard := renderConversation(kept, nil)
	if !strings.Contains(heard, "the harbour at dawn") || strings.Contains(heard, "a plain turn") {
		t.Fatalf("the first pass reads %s", heard)
	}
	for _, gone := range []string{"authority", "EXTERNAL_UNTRUSTED", "comes in a later pass"} {
		if strings.Contains(heard, gone) {
			t.Fatalf("the first pass reads %q: %s", gone, heard)
		}
	}
	if err := s.PublishConversationCursor(kept); err != nil {
		t.Fatal(err)
	}

	// The second: the rest, and nothing of the first again.
	second, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	heard = renderConversation(second, nil)
	if !strings.Contains(heard, "a plain turn after it") {
		t.Fatalf("the second pass lost the turn after it: %s", heard)
	}
	for _, gone := range []string{"harbour", "authority", "EXTERNAL_UNTRUSTED", "Someone spoke"} {
		if strings.Contains(heard, gone) {
			t.Fatalf("the second pass read %q again: %s", gone, heard)
		}
	}

}

// A NAMED VOICE'S TURN TOO LONG FOR THE MODEL HOLDS NOTHING UP. It is first in the pass and no
// limit will take it whole, so it is read in recorded parts, as a cursor already inside a turn
// reads it: every pass moves the cursor, every recorded letter is read once, and the turn after
// it is reached.
func TestANamedVoicesTurnTooLongForTheModelIsReadInRecordedParts(t *testing.T) {
	const named = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	s, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	framed := store.HeardFromSomeone("Same label", strings.TrimSpace(strings.Repeat("the harbour at dawn ", 200)))
	seq, err := s.AddConversationTurnSeq("participant", framed)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnnotateTurn(seq, store.AnnotationSpeaker, "vs-1/1", `{"speaker_uuid":"`+named+`","continuity":"matched","registry_revision":"9"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddConversationTurnSeq("operator", "a plain turn after it"); err != nil {
		t.Fatal(err)
	}
	s.SetOperatorVoices([]string{named})

	cfg := &DreamConfig{Threshold: 1}
	d := NewDream(&mockStore{}, &mockLLM{}, nil, nil, *cfg)
	limit := cursor.TurnLength(d.request(cfg, nil, cursor.ConversationBatch{}, nil)) + 1000
	ask := &admission{ctx: t.Context(), check: func(_ context.Context, _, user string) error {
		if cursor.TurnLength(user) > limit {
			return &wire.ContextLimitError{Required: 2, Limit: 1}
		}
		return nil
	}}
	var read strings.Builder
	passes := 0
	for ; passes < 20; passes++ {
		batch, err := s.NextConversation(100000, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(batch.Parts) == 0 {
			break
		}
		fitted, fits := d.fitConversation(cfg, ask, nil, batch, nil)
		if !fits || !fitted.Moved() {
			t.Fatalf("pass %d waits: fits %v, %+v", passes+1, fits, fitted)
		}
		for _, p := range fitted.Parts {
			read.WriteString(p.Text)
			if p.AsRecorded != nil || strings.Contains(p.Text, "the harbour") && p.Role == "operator" {
				t.Fatalf("pass %d read it rebuilt: %+v", passes+1, p)
			}
		}
		if err := s.PublishConversationCursor(fitted); err != nil {
			t.Fatal(err)
		}
	}
	if passes < 2 || passes == 20 {
		t.Fatalf("it took %d passes", passes)
	}
	if got, want := read.String(), framed+"a plain turn after it"; got != want {
		t.Errorf("the passes read %q, want each recorded letter once: %q", got, want)
	}
}

// Only the first turn of a pass may go over the budget, as today.
func TestARebuiltTurnHoldsTheSelectionBudget(t *testing.T) {
	const named = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	s, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, words := range []string{"a turn already read", "a plain turn first"} {
		if _, err := s.AddConversationTurnSeq("operator", words); err != nil {
			t.Fatal(err)
		}
	}
	seq, err := s.AddConversationTurnSeq("participant", store.HeardFromSomeone("Same label", "the harbour at dawn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnnotateTurn(seq, store.AnnotationSpeaker, "vs-1/1", `{"speaker_uuid":"`+named+`","continuity":"matched","registry_revision":"9"}`); err != nil {
		t.Fatal(err)
	}
	s.SetOperatorVoices([]string{named})
	all, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishConversationCursor(all.Keep(1, 0)); err != nil {
		t.Fatal(err)
	}
	batch, err := s.NextConversation(len("a plain turn first")+5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Parts) != 1 || batch.Parts[0].Text != "a plain turn first" {
		t.Fatalf("the selection over its budget: %+v", batch.Parts)
	}
}

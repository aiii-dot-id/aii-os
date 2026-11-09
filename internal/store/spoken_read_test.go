package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// The record's other reader: what the cognitive passes take in reads a named voice as
// the prompt does. The cursor still counts the record's own bytes, so the turn is not
// read again, and another voice's turn is as recorded.
func TestTheConversationIntakeReadsANamedVoiceAsThePromptDoes(t *testing.T) {
	const named, other = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f", "9078c44a-6fbe-4f24-a20e-bd212f520530"
	s, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, c := range []struct{ uuid, words string }{{named, "the harbour at dawn"}, {other, "the mountain at noon"}} {
		seq, err := s.AddConversationTurnSeq("participant", HeardFromSomeone("Same label", c.words))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AnnotateTurn(seq, AnnotationSpeaker, "vs-1/1", `{"speaker_uuid":"`+c.uuid+`","continuity":"matched","registry_revision":"9"}`); err != nil {
			t.Fatal(err)
		}
	}
	s.SetOperatorVoices([]string{named})
	batch, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Parts) != 2 {
		t.Fatalf("the intake holds %+v", batch.Parts)
	}
	want := `[voice · your operator's voice (speaker_uuid="` + named + `"; continuity=matched; registry_revision=9)] the harbour at dawn`
	if p := batch.Parts[0]; p.Role != "operator" || p.Text != want {
		t.Fatalf("a named voice's turn reads %+v", p)
	}
	if p := batch.Parts[1]; p.Role != "participant" || !strings.Contains(p.Text, "authority") {
		t.Fatalf("another voice's turn reads %+v", p)
	}
	if err := s.PublishConversationCursor(batch); err != nil {
		t.Fatal(err)
	}
	again, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Parts) != 0 {
		t.Fatalf("a read turn was read again: %+v", again.Parts)
	}
}

// A NAME GIVEN OR TAKEN BACK TELLS WHAT WATCHES THE RECORD, because how a page draws a spoken
// row follows it, whichever door it came through.
func TestSettingTheOperatorsVoicesTellsTheWatcher(t *testing.T) {
	s, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	told := 0
	s.SetInteractionObserver(func() { told++ })
	s.SetOperatorVoices([]string{"6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"})
	if told != 1 {
		t.Errorf("naming a voice told the watcher %d times", told)
	}
	s.SetOperatorVoices(nil)
	if told != 2 {
		t.Errorf("taking the name back left the watcher told %d times", told)
	}
}

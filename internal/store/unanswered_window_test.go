package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// unanswered_window_test.go — the note that words said during a failed
// turn went unanswered (T79) reaches the turn after the failure, beside
// the row it is about, and no later turn: "consumed" is the order of the
// rows already written, not a flag.

func TestTheWindowDeliversAnUnansweredNoteToTheNextTurnOnly(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	record := func(role, content, turn, related string, details interaction.Details) string {
		t.Helper()
		ref, err := s.RecordRelatedConversation(ctx, role, content, turn, related, details)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	none := interaction.Details{}
	unanswered := interaction.Details{ReasonCode: ReasonSteeringUnanswered}
	const note = "[said while that turn was working, and it ended before answering: «use the other file»]"

	// The failed turn: a question, words steered in, a partial reply, its
	// interruption marker and the note on the unanswered words.
	record("operator", "first question", "turn-1", "", none)
	record("operator", "use the other file", "turn-1", "", none)
	reply := record("resident", "I am reading", "turn-1", "", none)
	record("system", "[the reply above is incomplete — the substrate failed mid-turn]", "turn-1", reply, interaction.Details{ReasonCode: "interrupted"})
	record("system", "[some other notice about the reply]", "turn-1", reply, interaction.Details{ReasonCode: "other"})
	record("system", note, "turn-1", reply, unanswered)
	// Neither the failed turn's own later words nor words recorded with no
	// turn (a room's, heard while idle) are a turn that read the note.
	record("resident", "a late row of the failed turn", "turn-1", "", none)
	record("participant", "room words, no turn", "", "", none)

	carried := func(turn string) []string {
		t.Helper()
		turns, _, err := s.ConversationWindow(20, turn)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range turns {
			if len(r.Unanswered) > 0 && r.ID != reply {
				t.Fatalf("the note rides %q, not the reply it is about", r.Content)
			}
			got = append(got, r.Unanswered...)
		}
		return got
	}
	// Before the next turn has recorded anything (a timer's wake records
	// its fact as a system row): the turn being built is the first.
	if got := carried("turn-2"); !reflect.DeepEqual(got, []string{note}) {
		t.Fatalf("the first turn after the failure is not given the note: %q", got)
	}
	record("operator", "second question", "turn-2", "", none)
	if got := carried("turn-2"); !reflect.DeepEqual(got, []string{note}) {
		t.Fatalf("the turn after the failure is not given the note once: %q", got)
	}
	// THE CONTROL: any other turn reading the same window is not.
	if got := carried("turn-3"); len(got) != 0 {
		t.Fatalf("a turn the note was not delivered to was given it: %q", got)
	}
	record("resident", "noted", "turn-2", "", none)
	record("operator", "third question", "turn-3", "", none)
	if got := carried("turn-3"); len(got) != 0 {
		t.Fatalf("THE NOTE WAS REPLAYED to a later turn: %q", got)
	}
	// The turn it was delivered to still renders it, so that turn's request
	// is the same whenever it is rebuilt from the record.
	if got := carried("turn-2"); !reflect.DeepEqual(got, []string{note}) {
		t.Fatalf("the delivering turn's own view changed after the fact: %q", got)
	}
}

// A failed turn that said nothing has no reply: its note is about the
// words it last took in, and is carried beside them — a person's row,
// which the interruption markers never ride.
func TestAnUnansweredNoteRidesThePersonsWordsWhenNothingWasSaid(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	record := func(role, content, turn, related string, details interaction.Details) string {
		t.Helper()
		ref, err := s.RecordRelatedConversation(ctx, role, content, turn, related, details)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	record("operator", "first question", "turn-1", "", interaction.Details{})
	steer := record("operator", "[voice] stop", "turn-1", "", interaction.Details{})
	if id, err := s.LatestWords("turn-1"); err != nil || id != steer {
		t.Fatalf("the failed turn's last words = %q, %v; want the steered row", id, err)
	}
	if id, err := s.LatestWords("turn-none"); err != nil || id != "" {
		t.Fatalf("a turn with no words names %q, %v", id, err)
	}
	record("system", "a tool event, never words", "turn-1", "", interaction.Details{})
	if id, err := s.LatestWords(""); err != nil || id != steer {
		t.Fatalf("the conversation's newest words = %q, %v; want the steered row", id, err)
	}
	record("system", "[said while that turn was working, and it ended before answering: «[voice] stop»]", "turn-1", steer,
		interaction.Details{ReasonCode: ReasonSteeringUnanswered})
	record("operator", "second question", "turn-2", "", interaction.Details{})
	turns, total, err := s.ConversationWindow(20, "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(turns) != 3 {
		t.Fatalf("total=%d kept=%d, want 3 and 3: the note must not count toward the window", total, len(turns))
	}
	for _, r := range turns {
		want := []string(nil)
		if r.ID == steer {
			want = []string{"[said while that turn was working, and it ended before answering: «[voice] stop»]"}
		}
		if !reflect.DeepEqual(r.Unanswered, want) || len(r.Interruptions) != 0 {
			t.Fatalf("%q carries %q / %q, want %q", r.Content, r.Unanswered, r.Interruptions, want)
		}
	}
}

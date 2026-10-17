package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// The window's first turn changes once in n turns, never less than n
// turns are carried, never more than 2n−1.
func TestSteppedWindowMovesInSteps(t *testing.T) {
	const n = 4
	for total, want := range map[int]int{0: 0, 1: 1, 4: 4, 5: 5, 7: 7, 8: 4, 9: 5, 11: 7, 12: 4} {
		if got := SteppedWindow(total, n); got != want {
			t.Fatalf("SteppedWindow(%d, %d) = %d, want %d", total, n, got, want)
		}
	}
	if SteppedWindow(10, 0) != 0 {
		t.Fatal("a zero floor must carry no history, like RecentTurns(0)")
	}
	starts := map[int]bool{}
	for total := n; total < 5*n; total++ {
		keep := SteppedWindow(total, n)
		if keep < n || keep > 2*n-1 {
			t.Fatalf("total %d keeps %d, outside [%d, %d]", total, keep, n, 2*n-1)
		}
		starts[total-keep] = true
	}
	if len(starts) != 4 {
		t.Fatalf("over %d turns the window started at %d places, want one per step of %d", 4*n, len(starts), n)
	}
}

func TestConversationWindowReadsTheSteppedTail(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 9; i++ {
		if err := s.AddConversationTurn("operator", fmt.Sprintf("turn %d", i)); err != nil {
			t.Fatal(err)
		}
		if err := s.AddConversationTurn("system", "a tool event, never history"); err != nil {
			t.Fatal(err)
		}
	}
	turns, total, err := s.ConversationWindow(4, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 9 || len(turns) != 5 {
		t.Fatalf("total=%d kept=%d, want 9 and 5", total, len(turns))
	}
	if turns[0].Content != "turn 4" || turns[4].Content != "turn 8" {
		t.Fatalf("window = %q..%q, want turn 4..turn 8", turns[0].Content, turns[4].Content)
	}
}

func TestTheWindowCarriesInterruptionMarkersOnTheirReplies(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	record := func(role, content, related string, details interaction.Details) string {
		t.Helper()
		ref, err := s.RecordRelatedConversation(ctx, role, content, "", related, details)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	interrupted := interaction.Details{ReasonCode: "interrupted"}
	old := record("resident", "an old unfinished reply", "", interaction.Details{})
	record("system", "[the reply above is incomplete — old]", old, interrupted)
	for i := 0; i < 4; i++ {
		record("operator", fmt.Sprintf("filler %d", i), "", interaction.Details{})
	}
	asked := record("operator", "the question", "", interaction.Details{})
	record("system", "[a marker on a person's words]", asked, interrupted)
	reply := record("resident", "the unfinished reply", "", interaction.Details{})
	record("system", "[some other notice about the reply]", reply, interaction.Details{ReasonCode: "other"})
	record("system", "[the reply above is incomplete — stopped by the operator]", reply, interrupted)

	turns, total, err := s.ConversationWindow(3, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 || len(turns) != 4 {
		t.Fatalf("total=%d kept=%d, want 7 and 4: the markers must not count toward the window", total, len(turns))
	}
	for _, turn := range turns {
		var want []string
		if turn.ID == reply {
			want = []string{"[the reply above is incomplete — stopped by the operator]"}
		}
		if turn.ID == old {
			t.Fatal("the old reply should have left the window")
		}
		if !reflect.DeepEqual(turn.Interruptions, want) {
			t.Fatalf("%q carries %q, want %q", turn.Content, turn.Interruptions, want)
		}
	}
}

func TestATurnIsReadByItsSequenceAndAnAbsentOneIsNoError(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seq, err := s.AddConversationTurnSeq("operator", "the one turn")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTurnBySeq(seq)
	if err != nil || got == nil || got.Content != "the one turn" || got.Role != "operator" || got.TurnSeq != seq {
		t.Fatalf("the turn at %d reads as %+v (%v)", seq, got, err)
	}
	for _, absent := range []uint64{seq + 1, seq + 1000, 0} {
		turn, err := s.GetTurnBySeq(absent)
		if err != nil {
			t.Fatalf("sequence %d holds no turn, and that is an error: %v", absent, err)
		}
		if turn != nil {
			t.Fatalf("sequence %d holds no turn, yet one was read: %+v", absent, turn)
		}
	}
}

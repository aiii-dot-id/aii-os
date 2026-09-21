package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

// .
// .
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
	turns, total, err := s.ConversationWindow(4)
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

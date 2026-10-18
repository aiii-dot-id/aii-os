package store

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestOperatorActHistoryScopesBeforeLimitAndKeepsAnchorResults(t *testing.T) {
	s := testStore(t)
	add := func(turn, actor string) {
		t.Helper()
		if err := s.RecordToolStart(turn, 1, actor, "", "", "probe", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := recordToolDone(s, turn, 1, "probe", "{}", turn, interaction.Succeeded, false); err != nil {
			t.Fatal(err)
		}
	}
	add("target", "operator")
	anchor, err := s.RecordConversation(t.Context(), "operator", "confirmed", "target", interaction.Details{Actor: "operator", Channel: "plugin"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < interaction.MaxPageRows+1; i++ {
		add(fmt.Sprintf("ordinary-%d", i), "main")
	}
	rows, more, err := s.OperatorActResults(t.Context(), anchor.Sequence, []string{"target"})
	if err != nil || more || len(rows) != 1 || rows[0].TurnID != "target" {
		t.Fatalf("anchor or actor scope lost: %+v %v %v", rows, more, err)
	}
	if rows, _, err := s.OperatorActResults(t.Context(), anchor.Sequence, nil); err != nil || len(rows) != 0 {
		t.Fatalf("old unrelated results crossed floor: %+v %v", rows, err)
	}
	for i := 0; i < interaction.MaxPageRows+1; i++ {
		add(fmt.Sprintf("operator-%d", i), "operator")
	}
	rows, more, err = s.OperatorActResults(t.Context(), 0, nil)
	if err != nil || !more || len(rows) != interaction.MaxPageRows || rows[0].TurnID != "operator-1" {
		t.Fatalf("bounded newest page dishonest: %d %v %v", len(rows), more, err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Sequence >= rows[i].Sequence {
			t.Fatal("results not chronological")
		}
	}
}

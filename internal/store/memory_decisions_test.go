package store

import (
	"fmt"
	"testing"
)

func TestMemoryDecisionsAreLoggedAndBounded(t *testing.T) {
	s := testStore(t)
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "salience", Facility: "consolidate", Decision: "memo", Score: 0.2, Record: map[string]interface{}{"candidate": "the tide"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "rhythm", Facility: "consolidate", Decision: "run", Record: map[string]interface{}{"reason": "capacity"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "guess", Facility: "x", Decision: "y"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	got, err := s.RecentMemoryDecisions("salience", 10)
	if err != nil || len(got) != 1 || got[0].Decision != "memo" || got[0].Record["candidate"] != "the tide" || got[0].DecidedAt.IsZero() {
		t.Fatalf("salience decisions = %+v %v", got, err)
	}
	old := memoryDecisionsKeep
	memoryDecisionsKeep = 40
	defer func() { memoryDecisionsKeep = old }()
	for i := 0; i < memoryDecisionsKeep+20; i++ {
		if err := s.RecordMemoryDecision(MemoryDecision{Kind: "rhythm", Facility: "dream", Decision: "skip", Record: map[string]interface{}{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_decisions WHERE kind = 'rhythm'`).Scan(&n); err != nil || n != memoryDecisionsKeep {
		t.Fatalf("rhythm decisions kept = %d (%v), want the bound %d", n, err, memoryDecisionsKeep)
	}
	newest, _ := s.RecentMemoryDecisions("rhythm", 1)
	if fmt.Sprint(newest[0].Record["i"]) != fmt.Sprint(memoryDecisionsKeep+19) {
		t.Fatalf("the newest must survive the prune: %+v", newest[0].Record)
	}
}

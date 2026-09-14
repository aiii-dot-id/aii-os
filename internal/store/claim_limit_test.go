package store

import (
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .
func TestClaimHonorsPerKindRunningLimit(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetClaimLimit("subagent.run", 2)
	for i := 0; i < 3; i++ {
		if _, err := s.EnqueueWork(&WorkItem{Kind: "subagent.run", Payload: "{}", Source: "identity"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnqueueWork(&WorkItem{Kind: "alarm.rhythm", Payload: "{}", Source: "time"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	a, _ := s.ClaimWork([]string{"subagent.run"}, now)
	b, _ := s.ClaimWork([]string{"subagent.run"}, now)
	if a == nil || b == nil {
		t.Fatal("two claims must succeed under a cap of two")
	}
	if c, _ := s.ClaimWork([]string{"subagent.run"}, now); c != nil {
		t.Fatalf("the third claim must wait for a slot, got %v", c.ID)
	}
	if o, _ := s.ClaimWork(nil, now); o == nil || o.Kind != "alarm.rhythm" {
		t.Fatalf("another kind must still be claimable, got %+v", o)
	}
	if n, _ := s.RunningWorkCount("subagent.run"); n != 2 {
		t.Fatalf("running count = %d, want 2", n)
	}
	if err := s.CompleteWork(a.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.ClaimWork([]string{"subagent.run"}, now); c == nil {
		t.Fatal("a freed slot must admit the waiting item")
	}
	s.SetClaimLimit("subagent.run", 0)
	if _, err := s.EnqueueWork(&WorkItem{Kind: "subagent.run", Payload: "{}", Source: "identity"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.ClaimWork([]string{"subagent.run"}, now); c == nil {
		t.Fatal("with the cap removed the fourth item must be claimable")
	}
}

// .
func TestSubagentQueueStatesKeyBySession(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, k := range []string{"ws_a", "ws_b#2"} {
		if _, err := s.EnqueueWork(&WorkItem{Kind: "subagent.run", Payload: "{}", Source: "identity", DedupKey: k}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimWork([]string{"subagent.run"}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	states, err := s.SubagentQueueStates("subagent.run")
	if err != nil {
		t.Fatal(err)
	}
	if states["ws_a"] != "CLAIMED" || states["ws_b"] != "PENDING" {
		t.Fatalf("states = %v", states)
	}
}

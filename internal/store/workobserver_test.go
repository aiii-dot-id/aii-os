package store

import "testing"

// .
// .
// .
func TestWorkObserverHearsBoundariesOutsideTheLock(t *testing.T) {
	s := testStore(t)
	var got []WorkEvent
	s.SetWorkObserver(func(ev WorkEvent) {
		// .
		// .
		if _, err := s.ActiveWorkSession(); err != nil {
			t.Errorf("observer could not read the store: %v", err)
		}
		got = append(got, ev)
	})
	if err := s.StartWorkSession("ws_main", "write the report"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession("ws_child", subagentDescriptionPrefix+"gather the numbers"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession("ws_child", "served: the numbers", EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHarvested("ws_child", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession("ws_main", "partial: most of it", EvidenceCompletedLocally, ""); err != nil {
		t.Fatal(err)
	}
	// .
	if err := s.DeliverWorkSession("ws_main", "served: again", EvidenceCompletedLocally, ""); err == nil {
		t.Fatal("a second delivery over a real result must be refused")
	}
	// .
	if err := s.MarkHarvested("ws_absent", 1); err != nil {
		t.Fatal(err)
	}
	want := []WorkEvent{
		{Kind: WorkStarted, ID: "ws_main", Actor: "main"},
		{Kind: WorkStarted, ID: "ws_child", Actor: "subagent"},
		{Kind: WorkDelivered, ID: "ws_child", Actor: "subagent", Outcome: "served", Evidence: EvidenceWorkerReportOnly},
		{Kind: WorkHarvested, ID: "ws_child", Actor: "subagent"},
		{Kind: WorkDelivered, ID: "ws_main", Actor: "main", Outcome: "partial", Evidence: EvidenceCompletedLocally},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if outcomeClass("FAILED: interrupted by a runtime restart before delivery") != "failed" || outcomeClass("unserved: x") != "unserved" || outcomeClass("nothing") != "" {
		t.Fatal("outcome classes")
	}
	s.SetWorkObserver(nil)
	if err := s.StartWorkSession("ws_quiet", "quiet"); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal("a cleared observer hears nothing")
	}
}

package store

import (
	"path/filepath"
	"testing"
)

func TestHarvestedMarkerLifecycle(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id := "ws_test_1"
	queueChild(t, s, id, "DONE")
	if err := s.StartWorkSession(t.Context(), id, subagentDescriptionPrefix+"review the boundary"); err != nil {
		t.Fatal(err)
	}
	// The lawful re-delivery path starts from the pre-execution marker.
	if err := s.DeliverWorkSession(t.Context(), id, "unserved: failed before start: compose blew up", "", ""); err != nil {
		t.Fatal(err)
	}

	unh, err := s.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) != 1 || unh[0].ID != id {
		t.Fatalf("unharvested after delivery = %+v (want exactly [%s])", unh, id)
	}

	if err := s.MarkHarvested(id, 12345); err != nil {
		t.Fatal(err)
	}
	unh, err = s.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) != 0 {
		t.Fatalf("unharvested after marking = %d (want 0)", len(unh))
	}

	// Re-delivery re-arms: the retry's successful outcome must surface
	// as unharvested again despite the failed attempt's earlier mark.
	if err := s.DeliverWorkSession(t.Context(), id, "outcome text v2 (retry succeeded)", "", ""); err != nil {
		t.Fatal(err)
	}
	unh, err = s.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) != 1 || unh[0].Result != "outcome text v2 (retry succeeded)" {
		t.Fatalf("re-delivery did not re-arm: %+v", unh)
	}
}

package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestHarvestedMarkerLifecycle(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id := "ws_test_1"
	if err := s.StartWorkSession(id, subagentDescriptionPrefix+"review the boundary"); err != nil {
		t.Fatal(err)
	}
	// .
	if err := s.DeliverWorkSession(id, "unserved: failed before start: compose blew up", "", ""); err != nil {
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

	// .
	// .
	if err := s.DeliverWorkSession(id, "outcome text v2 (retry succeeded)", "", ""); err != nil {
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

package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHarvestMarkerRearmsOnRedelivery(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), string([]byte{97, 105, 105, 46, 100, 98})))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sid := "ws_retry_edge"
	queueChild(t, s, sid, "DONE")
	if err := s.StartWorkSession(t.Context(), sid, "sub-agent: rearm edge"); err != nil {
		t.Fatal(err)
	}

	// First delivery: the failure path. Marker arms on compose.
	if err := s.DeliverWorkSession(t.Context(), sid, "unserved: failed before start: context deadline exceeded", "", ""); err != nil {
		t.Fatal(err)
	}
	unh0, err := s.UnharvestedDeliveries(8)
	if err != nil || len(unh0) != 1 {
		t.Fatalf("first delivery must surface: %v %d", err, len(unh0))
	}
	if err := s.MarkHarvested(unh0[0].ID, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Sanity: marker holds across an ordinary subsequent compose.
	unh1, err := s.UnharvestedDeliveries(8)
	if err != nil || len(unh1) != 0 {
		t.Fatalf("marked delivery must not resurface: %v %d", err, len(unh1))
	}

	// THE EDGE: the retry succeeds and re-delivers through the same
	// DeliverWorkSession. The NULL in its UPDATE is the re-arm.
	if err := s.DeliverWorkSession(t.Context(), sid, "DONE: the retry succeeded where the first attempt failed", "", ""); err != nil {
		t.Fatal(err)
	}
	unh2, err := s.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh2) != 1 {
		t.Fatalf("re-delivery must resurface the session — the marker from the failure must not swallow the retry's success: %d", len(unh2))
	}
	if unh2[0].Result == "unserved: failed before start: context deadline exceeded" {
		t.Fatalf("resurfaced row carries the STALE failure text, not the retry outcome:\n%s", unh2[0].Result)
	}
}

package store

import "testing"

// A runtime that dies mid-work leaves its work_sessions rows 'active'
// forever: the dashboard shows phantom work and the identity believes
// a hand it no longer has. Boot closes them honestly.
func TestOrphanedActiveWorkSessionsAreSwept(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession("ws_orphan", "long survey"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.StartWorkSession("ws_done", "quick check"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession("ws_done", "42"); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	n, err := s.SweepOrphanWorkSessions()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d session(s), want exactly the orphan", n)
	}
	// Idempotent: a second boot finds nothing active.
	n, err = s.SweepOrphanWorkSessions()
	if err != nil || n != 0 {
		t.Fatalf("second sweep: n=%d err=%v", n, err)
	}
}

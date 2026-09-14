package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestStandingStateIsASingletonThatEmptyClears(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// .
	got, err := s.StandingState()
	if err != nil || got != "" {
		t.Fatalf("virgin store returned (%q, %v), want (\"\", nil)", got, err)
	}

	if err := s.SetStandingState("holding for the 17:25Z telemetry fire"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.StandingState(); got != "holding for the 17:25Z telemetry fire" {
		t.Fatalf("after set: %q", got)
	}

	// .
	if err := s.SetStandingState("second answer"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.StandingState(); got != "second answer" {
		t.Fatalf("after overwrite: %q", got)
	}
	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM standing_state`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("standing_state holds %d rows, want exactly 1 — it is a singleton", rows)
	}

	// .
	if err := s.SetStandingState(""); err != nil {
		t.Fatalf("clearing must be allowed: %v", err)
	}
	if got, _ = s.StandingState(); got != "" {
		t.Fatalf("after clear: %q", got)
	}
}

// .
// .
// .
func TestStandingStateSurvivesReplay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetStandingState("a wait that must outlive a rebuild"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayAll(nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	got, err := s.StandingState()
	if err != nil {
		t.Fatal(err)
	}
	if got != "a wait that must outlive a rebuild" {
		t.Fatalf("replay erased standing state (%q) — it is not a projection and must survive", got)
	}
}

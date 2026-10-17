package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The surface that outlives both a project and a session. Empty is a
// legitimate value ("nothing stands"), never an error, and the row is a
// singleton: one answer to "what am I doing", overwritten not versioned.
func TestStandingStateIsASingletonThatEmptyClears(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Never written: "" and no error. A blank surface is not a failure.
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

	// Overwrite, not append — one answer, not a history.
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

	// Empty CLEARS and is stored, not refused.
	if err := s.SetStandingState(""); err != nil {
		t.Fatalf("clearing must be allowed: %v", err)
	}
	if got, _ = s.StandingState(); got != "" {
		t.Fatalf("after clear: %q", got)
	}
}

// Ring 4, not f(ledger): ReplayAll clears projections and must not take
// standing state with them. replay.go names the class explicitly —
// conversations, work_sessions, outbox, alarms are NOT projections.
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

// The standing text comes back with the time it was authored, so the
// prompt can date it; StandingState itself answers exactly as before.
func TestStandingStateAuthoredCarriesItsTime(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	text, ms, err := s.StandingStateAuthored()
	if err != nil || text != "" || ms != 0 {
		t.Fatalf("virgin store returned (%q, %d, %v), want (\"\", 0, nil)", text, ms, err)
	}
	before := time.Now().UTC().UnixMilli()
	if err := s.SetStandingState("holding for the review"); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().UnixMilli()
	text, ms, err = s.StandingStateAuthored()
	if err != nil || text != "holding for the review" || ms < before || ms > after {
		t.Fatalf("after set: (%q, %d, %v), want the text and a time in [%d, %d]", text, ms, err, before, after)
	}
	if got, err := s.StandingState(); err != nil || got != text {
		t.Fatalf("StandingState = (%q, %v), want the same text", got, err)
	}
}

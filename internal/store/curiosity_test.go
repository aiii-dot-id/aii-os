package store

import (
	"strings"
	"testing"
)

// .
// .
func TestCuriosityCueRoundTripAndClear(t *testing.T) {
	s := testStore(t)
	if c, err := s.CuriosityCue(); err != nil || c != nil {
		t.Fatalf("no cue expected initially: %v %+v", err, c)
	}
	if err := s.SetCuriosityCue("the shape of tail latency", "it went bimodal under load", "ws_probe", "invitation"); err != nil {
		t.Fatalf("set: %v", err)
	}
	c, err := s.CuriosityCue()
	if err != nil || c == nil {
		t.Fatalf("readback: %v %+v", err, c)
	}
	if c.Subject != "the shape of tail latency" || c.Why == "" || c.Pointer != "ws_probe" || c.Kind != "invitation" || c.SetAt == "" {
		t.Fatalf("cue not persisted with provenance: %+v", c)
	}
	// .
	if err := s.SetCuriosityCue("something else entirely", "", "", ""); err != nil {
		t.Fatalf("replace: %v", err)
	}
	c, _ = s.CuriosityCue()
	if c.Subject != "something else entirely" || c.Kind != "invitation" {
		t.Fatalf("replace failed: %+v", c)
	}
	if err := s.ClearCuriosityCue(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c, _ := s.CuriosityCue(); c != nil {
		t.Fatalf("cleared cue still present: %+v", c)
	}
}

// .
func TestRenderCuriosityCueIsAnInvitationNotATask(t *testing.T) {
	out := RenderCuriosityCue(CuriosityCue{Subject: "tail latency", Why: "bimodal", Pointer: "ws_x", Kind: "invitation"})
	for _, want := range []string{"Curiosity", "invitation, not a task", "tail latency", "closes nothing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q: %q", want, out)
		}
	}
	for _, banned := range []string{"acceptance", "obligation", "TODO"} {
		if strings.Contains(out, banned) {
			t.Fatalf("cue must not read as an obligation (%q): %q", banned, out)
		}
	}
	if RenderCuriosityCue(CuriosityCue{}) != "" {
		t.Fatal("empty subject renders nothing — the cue is never fabricated")
	}
}

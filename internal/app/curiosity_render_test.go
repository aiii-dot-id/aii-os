package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestCuriosityCueRendersAndIsSuppressed(t *testing.T) {
	a, _ := focusFixture(t)

	// absent when no cue exists (never fabricated).
	state, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state, "Curiosity") {
		t.Fatalf("no cue set, yet a curiosity line appeared:\n%s", state)
	}

	// present when set and nothing urgent is owed.
	if err := a.store.SetCuriosityCue("the shape of tail latency", "bimodal under load", "ws_probe", "invitation"); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildTurnFacts(t.Context(), false)
	if !strings.Contains(state, "Curiosity") || !strings.Contains(state, "the shape of tail latency") {
		t.Fatalf("a set cue must resurface as an invitation:\n%s", state)
	}

	// suppressed when an operator decision is owed — the decision shows
	// instead of the cue.
	if err := a.store.StartWorkSession(t.Context(), "ws_1", "some work"); err != nil {
		t.Fatal(err)
	}
	focus := "the active concern"
	dn := "confirm the endpoint before acting"
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_1", &focus, nil, nil, nil, nil, &dn); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildTurnFacts(t.Context(), false)
	if strings.Contains(state, "Curiosity") {
		t.Fatalf("an operator decision must suppress the cue:\n%s", state)
	}
	// The decision is authored, so it stays in the working state (the
	// system prompt); the cue it displaces rides the turn's facts.
	authored, _ := a.buildWorkState()
	if !strings.Contains(authored, "Decision needed") {
		t.Fatalf("the owed decision must be shown in its place:\n%s", authored)
	}
}

func TestAnUnreadableCuriosityCueIsLoggedNotHidden(t *testing.T) {
	a, _ := focusFixture(t)
	if err := a.store.SetCuriosityCue("the shape of tail latency", "bimodal under load", "ws_probe", "invitation"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB().Exec(`DROP TABLE curiosity_cue`); err != nil {
		t.Fatal(err)
	}
	capture := logsink.CaptureForTest(t)
	state, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatalf("an unreadable voluntary invitation must not fail the turn: %v", err)
	}
	if strings.Contains(state, "Curiosity") {
		t.Fatalf("a cue that could not be read was rendered:\n%s", state)
	}
	if !capture.Contains("curiosity cue unreadable this turn") {
		t.Fatalf("the failed read was not logged:\n%s", capture.String())
	}
}

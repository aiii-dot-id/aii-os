package app

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestCuriosityCueRendersAndIsSuppressed(t *testing.T) {
	a, _ := focusFixture(t)

	// .
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state, "Curiosity") {
		t.Fatalf("no cue set, yet a curiosity line appeared:\n%s", state)
	}

	// .
	if err := a.store.SetCuriosityCue("the shape of tail latency", "bimodal under load", "ws_probe", "invitation"); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildWorkState()
	if !strings.Contains(state, "Curiosity") || !strings.Contains(state, "the shape of tail latency") {
		t.Fatalf("a set cue must resurface as an invitation:\n%s", state)
	}

	// .
	// .
	if err := a.store.StartWorkSession("ws_1", "some work"); err != nil {
		t.Fatal(err)
	}
	focus := "the active concern"
	dn := "confirm the endpoint before acting"
	if err := a.store.UpdateWorkPlan("ws_1", &focus, nil, nil, nil, nil, &dn); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildWorkState()
	if strings.Contains(state, "Curiosity") {
		t.Fatalf("an operator decision must suppress the cue:\n%s", state)
	}
	if !strings.Contains(state, "Decision needed") {
		t.Fatalf("the owed decision must be shown in its place:\n%s", state)
	}
}

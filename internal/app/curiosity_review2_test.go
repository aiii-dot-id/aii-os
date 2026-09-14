package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
func TestReview2CuriositySuppressedByUnharvestedRecovery(t *testing.T) {
	a, _ := focusFixture(t)
	if err := a.store.SetCuriosityCue("tail latency", "bimodal", "ws_probe", "invitation"); err != nil {
		t.Fatal(err)
	}
	// .
	id := "ws_child"
	if err := a.store.StartWorkSession(id, store.SubagentDescription("count the beans")); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession(id, "unserved: could not reach the jar", store.EvidenceExternalUnknown, ""); err != nil {
		t.Fatal(err)
	}
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	// .
	if !strings.Contains(state, "recovery:") {
		t.Fatalf("the owed sub-agent recovery must render:\n%s", state)
	}
	// .
	if strings.Contains(state, "### Curiosity") {
		t.Fatalf("an owed sub-agent recovery must suppress the curiosity cue:\n%s", state)
	}
}

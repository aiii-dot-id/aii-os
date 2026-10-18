package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestReview2CuriositySuppressedByUnharvestedRecovery(t *testing.T) {
	a, _ := focusFixture(t)
	if err := a.store.SetCuriosityCue("tail latency", "bimodal", "ws_probe", "invitation"); err != nil {
		t.Fatal(err)
	}
	// a delivered sub-agent whose outcome owes a read-only recovery.
	id := "ws_child"
	queueChild(t, a.store, id)
	if err := a.store.StartWorkSession(t.Context(), id, store.SubagentDescription("count the beans")); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession(t.Context(), id, "unserved: could not reach the jar", store.EvidenceExternalUnknown, ""); err != nil {
		t.Fatal(err)
	}
	state, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	// the recovery must be shown...
	if !strings.Contains(state, "recovery:") {
		t.Fatalf("the owed sub-agent recovery must render:\n%s", state)
	}
	// ...and the voluntary cue suppressed.
	if strings.Contains(state, "### Curiosity") {
		t.Fatalf("an owed sub-agent recovery must suppress the curiosity cue:\n%s", state)
	}
}

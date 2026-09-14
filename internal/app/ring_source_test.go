package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
func TestFacilityGateSourcesRenderTheSectionsTheIdentityReads(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern.")
	rm.SetSection(ring.Ring3, "working_truth", "You believe the anchor held.")
	src := appRingSource{rm: rm, priorities: fakePriorities{"Verify the gate."}}
	r3 := src.Ring3()
	if !strings.Contains(r3, "You may be noticing a pattern.") || !strings.Contains(r3, "You believe the anchor held.") {
		t.Fatalf("Ring 3 for facilities is not the identity's working truth: %q", r3)
	}
	if strings.Index(r3, "noticing") > strings.Index(r3, "anchor held") {
		t.Fatal("the facility sees working truth in a different order than the identity")
	}
	if r4 := src.Ring4(); !strings.Contains(r4, "Verify the gate") {
		t.Fatalf("Ring 4 for facilities is not the priorities: %q", r4)
	}
	if empty := (appRingSource{rm: ring.NewManager()}); empty.Ring3() != "" || empty.Ring4() != "" {
		t.Fatal("an identity with no working truth must give the facilities nothing, not a frame")
	}
	if none := (appRingSource{rm: rm, priorities: fakePriorities{}}); none.Ring4() != "" {
		t.Fatal("no active intention, yet the facilities were handed priorities")
	}
}

// .
type fakePriorities []string

func (f fakePriorities) ActivePriorities() ([]string, error) { return []string(f), nil }

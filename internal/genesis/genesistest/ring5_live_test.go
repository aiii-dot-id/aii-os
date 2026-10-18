package genesistest

import (
	"strings"
	"testing"
)

// The fixture's Ring 5 is the platform's, verified by the production rule.
func TestTheFixturesRing5IsTheLivePosture(t *testing.T) {
	r5 := NewRoot(t).Ring5(t)
	if strings.TrimSpace(r5) == "" || strings.Contains(r5, "test fixture") {
		t.Fatalf("the fixture's Ring 5 is not a live posture: %q", r5[:min(len(r5), 80)])
	}
	t.Logf("live Ring 5: %d bytes", len(r5))
}

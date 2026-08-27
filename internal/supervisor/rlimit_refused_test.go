package supervisor

import (
	"errors"
	"strings"
	"testing"
)

// An operator who configures a memory ceiling and does not get one must
// not be left with a plugin they believe is bounded. Logged-and-continue
// was fail-open: the telemetry said "child runs unenveloped" and the
// child ran.
//
// A platform with NO mechanism is a different fact — recorded, and it
// does not ground the plugin, or macOS and Windows would run nothing.
func TestARefusedEnvelopeGroundsThePlugin(t *testing.T) {
	saved := applyLimit
	t.Cleanup(func() { applyLimit = saved })
	applyLimit = func(int, uint64) (string, error) {
		return "", errors.New("prlimit: operation not permitted")
	}

	_, lg := newCapture()
	spec := childSpec("respond", lg)
	spec.RLimitASBytes = 1 << 30
	spec.Backoff.MaxRestarts = 0

	s, err := Start(spec, nil)
	if err == nil {
		s.Close()
		t.Fatal("a plugin whose memory envelope was refused started anyway")
	}
	var refused *SpawnRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("the refusal is not a SpawnRefusedError, so the restart policy cannot read it: %v", err)
	}
	if !strings.Contains(err.Error(), "prlimit") {
		t.Fatalf("the refusal does not say what could not be applied: %v", err)
	}
}

// The no-mechanism platforms report and run. Same call, opposite answer,
// and conflating the two is what would ground every non-Linux host.
func TestAPlatformWithNoMechanismStillRuns(t *testing.T) {
	saved := applyLimit
	t.Cleanup(func() { applyLimit = saved })
	applyLimit = func(_ int, b uint64) (string, error) {
		return "RLIMIT_AS requested but NOT ENFORCED on this platform", nil
	}

	cap, lg := newCapture()
	spec := childSpec("respond", lg)
	spec.RLimitASBytes = 1 << 30

	s, err := Start(spec, nil)
	if err != nil {
		t.Fatalf("a platform with no rlimit mechanism could not run a plugin: %v", err)
	}
	defer s.Close()
	if !strings.Contains(cap.String(), "NOT ENFORCED") {
		t.Fatalf("the envelope gap was not reported to the operator: %s", cap.String())
	}
}

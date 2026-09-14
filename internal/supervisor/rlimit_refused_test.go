package supervisor

import (
	"errors"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
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

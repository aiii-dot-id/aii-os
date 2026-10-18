package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// The daemon's environment is where secrets live: the broker serves
// secret.read by reading os.Getenv(profile.SecretEnv). A child that
// inherits that environment reads the same variable without asking,
// which is a capability decision routed around rather than made. Canon
// does not let trust waive this one — proc.spawn and secret.read need
// explicit policy.json admission at every tier, T3 included.
func TestAChildDoesNotInheritTheDaemonsEnvironment(t *testing.T) {
	t.Setenv("AII_SUPERVISOR_CANARY", "every-key-the-daemon-holds")

	cap := logsink.CaptureForTest(t)
	s, err := Start(childSpec("respond"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(cap.String(), "child-ready") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := cap.String()
	if !strings.Contains(out, "plugins.session: plugin org.example.respond: fakechild: child-ready") {
		t.Fatalf("the child never reported ready: %s", out)
	}
	if strings.Contains(out, "every-key-the-daemon-holds") {
		t.Fatalf("the child inherited the daemon environment: %s", out)
	}
	if !strings.Contains(out, `canary=""`) {
		t.Fatalf("the child did not report an empty canary, so this proves nothing: %s", out)
	}
	// The variables the spec DOES name must still arrive, or the cut
	// broke the binding instead of the leak.
	if !strings.Contains(out, "socket=stdio:") {
		t.Fatalf("spec.Env did not reach the child: %s", out)
	}
}

// A spec that names no environment is the same question with no answer
// written down, and os/exec answers a nil environment with the parent's.
// The WASM worker's spec was that spec.
func TestAChildWhoseSpecNamesNoEnvironmentIsGivenNone(t *testing.T) {
	t.Setenv("AII_SUPERVISOR_CANARY", "every-key-the-daemon-holds")

	cap := logsink.CaptureForTest(t)
	spec := childSpec("respond")
	spec.Env = nil
	s, err := Start(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(cap.String(), "child-ready") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := cap.String()
	if !strings.Contains(out, "fakechild: child-ready") {
		t.Fatalf("the child never reported ready: %s", out)
	}
	if strings.Contains(out, "every-key-the-daemon-holds") {
		t.Fatalf("a spec with no environment handed the child the daemon's: %s", out)
	}
	// Both answers are read, so an empty canary is the child's own report
	// and not a line that was never written.
	if !strings.Contains(out, `socket= canary=""`) {
		t.Fatalf("the child did not report an empty environment: %s", out)
	}
}

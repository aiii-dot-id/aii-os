package supervisor

import (
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
func TestAChildDoesNotInheritTheDaemonsEnvironment(t *testing.T) {
	t.Setenv("AII_SUPERVISOR_CANARY", "every-key-the-daemon-holds")

	cap, lg := newCapture()
	s, err := Start(childSpec("respond", lg), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(cap.String(), "child-ready") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := cap.String()
	if !strings.Contains(out, "child-ready") {
		t.Fatalf("the child never reported ready: %s", out)
	}
	if strings.Contains(out, "every-key-the-daemon-holds") {
		t.Fatalf("the child inherited the daemon environment: %s", out)
	}
	if !strings.Contains(out, `canary=""`) {
		t.Fatalf("the child did not report an empty canary, so this proves nothing: %s", out)
	}
	// .
	// .
	if !strings.Contains(out, "socket=stdio:") {
		t.Fatalf("spec.Env did not reach the child: %s", out)
	}
}

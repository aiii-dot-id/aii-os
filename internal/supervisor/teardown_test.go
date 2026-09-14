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

func stuckChild() *child {
	return &child{exited: make(chan struct{})}
}

func TestTeardownDoesNotWaitForever(t *testing.T) {
	saved := closeGraceKill
	closeGraceKill = 50 * time.Millisecond
	t.Cleanup(func() { closeGraceKill = saved })

	cap, lg := newCapture()
	s := &Supervisor{spec: Spec{PluginID: "org.example.stuck", Log: lg}}

	done := make(chan bool, 1)
	go func() { done <- s.awaitReaped(stuckChild()) }()

	select {
	case reaped := <-done:
		if reaped {
			t.Fatal("a child that never exited was reported reaped")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("teardown waited past its own bound — the hang is still there")
	}

	out := cap.String()
	if !strings.Contains(out, "ORPHANED") || !strings.Contains(out, "org.example.stuck") {
		t.Fatalf("an abandoned process was not reported to the operator: %s", out)
	}
}

// .
// .
func TestTeardownStillWaitsForAChildThatExits(t *testing.T) {
	saved := closeGraceKill
	closeGraceKill = 2 * time.Second
	t.Cleanup(func() { closeGraceKill = saved })

	_, lg := newCapture()
	s := &Supervisor{spec: Spec{PluginID: "org.example.tidy", Log: lg}}
	c := stuckChild()
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(c.exited)
	}()
	if !s.awaitReaped(c) {
		t.Fatal("a child that exited normally was reported orphaned")
	}
}

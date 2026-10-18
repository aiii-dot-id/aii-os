package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// SIGKILL cannot be refused, but it is not instant: a child parked in
// uninterruptible I/O is not reaped until that I/O returns. Both
// teardown paths ended in a bare <-c.exited, so one stuck plugin could
// hang the whole application's shutdown with no message at all.

func stuckChild() *child {
	return &child{exited: make(chan struct{})} // never closed: never reaped
}

func TestTeardownDoesNotWaitForever(t *testing.T) {
	saved := closeGraceKill
	closeGraceKill = 50 * time.Millisecond
	t.Cleanup(func() { closeGraceKill = saved })

	cap := logsink.CaptureForTest(t)
	s := &Supervisor{spec: Spec{PluginID: "org.example.stuck"}}

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
	if !strings.Contains(out, "plugins.error: plugin org.example.stuck: SIGKILL did not reap") || !strings.Contains(out, "ORPHANED") {
		t.Fatalf("an abandoned process was not reported to the operator: %s", out)
	}
}

// A child that DOES exit must still be waited for — the bound is a
// backstop, not a shortcut that abandons healthy teardown early.
func TestTeardownStillWaitsForAChildThatExits(t *testing.T) {
	saved := closeGraceKill
	closeGraceKill = 2 * time.Second
	t.Cleanup(func() { closeGraceKill = saved })

	s := &Supervisor{spec: Spec{PluginID: "org.example.tidy"}}
	c := stuckChild()
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(c.exited)
	}()
	if !s.awaitReaped(c) {
		t.Fatal("a child that exited normally was reported orphaned")
	}
}

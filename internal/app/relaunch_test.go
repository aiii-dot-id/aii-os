package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Restart is asked once, however many times it is clicked: Run's select
// is released and the relaunch is owed.
func TestRestartIsAskedOnceAndReleasesRun(t *testing.T) {
	a := New(defaultConfig())
	if a.restarting.Load() {
		t.Fatal("not restarting before it is asked")
	}
	if err := a.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := a.Restart(); err != nil {
		t.Fatal("a second ask is the same ask")
	}
	select {
	case <-a.restartCh:
	default:
		t.Fatal("Run's select is not released")
	}
	if !a.restarting.Load() {
		t.Fatal("Run would not relaunch")
	}
}

// A RESTART THAT MEETS A TEARDOWN ERROR IS STILL HANDED OVER. Run tested
// the teardown's error before it looked at whether a restart was asked: a
// plugin that would not stop in time turned a Relaunch into exit 1, and
// on Windows, or for an identity started by hand, nothing started it
// again.
func TestARestartThatMeetsATeardownErrorIsStillHandedOver(t *testing.T) {
	failed := errors.New("plugins that would not stop: voice")
	for _, c := range []struct {
		name     string
		teardown error
		restart  bool
		want     string
	}{
		{"a clean stop returns", nil, false, ""},
		{"a failed stop leaves with 1", failed, false, "exit 1"},
		{"a clean restart hands over", nil, true, "hand over"},
		{"A RESTART AFTER A FAILED TEARDOWN HANDS OVER, and says the teardown failed", failed, true, "hand over, teardown failed"},
	} {
		var did []string
		relaunch := func(teardownFailed bool) {
			if teardownFailed {
				did = append(did, "hand over, teardown failed")
			} else {
				did = append(did, "hand over")
			}
		}
		exit := func(code int) { did = append(did, fmt.Sprintf("exit %d", code)) }
		leaveAfterShutdown(c.teardown, c.restart, relaunch, exit)
		if got := strings.Join(did, "; "); got != c.want {
			t.Errorf("%s: did %q, want %q", c.name, got, c.want)
		}
	}
}

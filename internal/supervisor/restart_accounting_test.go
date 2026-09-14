package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
func TestAHostRequestedKillIsNotCounted(t *testing.T) {
	_, lg := newCapture()
	spec := childSpec("sleep", lg)
	spec.Backoff.MaxRestarts = 2
	s := mustStart(t, spec, nil)
	for i := 0; i < 5; i++ {
		pid := s.Pid()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, err := s.Invoke(ctx, []byte(testReq))
		cancel()
		var terr *InvokeTimeoutError
		if !errors.As(err, &terr) {
			t.Fatalf("cancelled call %d: want InvokeTimeoutError, got %v", i, err)
		}
		waitFor(t, fmt.Sprintf("revival after cancelled call %d", i), func() bool {
			st, _ := s.State()
			return st == StateRunning && s.Pid() != 0 && s.Pid() != pid
		})
	}
	if n := s.Restarts(); n != 0 {
		t.Fatalf("host-requested kills were counted as crashes: restarts=%d", n)
	}
	if st, _ := s.State(); st != StateRunning {
		t.Fatalf("after five cancelled calls the plugin is %s, want running", st)
	}
}

// .
// .
// .
// .
// .
func TestCrashRestartsCountWithinTheWindowOnly(t *testing.T) {
	_, lg := newCapture()
	spec := childSpec("crash-after-respond", lg)
	spec.Backoff.MaxRestarts = 2
	spec.Backoff.Window = 150 * time.Millisecond
	s := mustStart(t, spec, nil)
	for i := 0; i < 4; i++ {
		pid := s.Pid()
		if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
			t.Fatalf("crash %d: the child answers before it exits: %v", i, err)
		}
		waitFor(t, fmt.Sprintf("revival after crash %d", i), func() bool {
			st, _ := s.State()
			return st == StateRunning && s.Pid() != 0 && s.Pid() != pid
		})
		time.Sleep(300 * time.Millisecond)
	}
	if st, _ := s.State(); st != StateRunning {
		t.Fatalf("four crashes spread wider than the window deactivated the plugin: %s", st)
	}
	if n := s.Restarts(); n != 4 {
		t.Fatalf("each crash is still a counted restart on the record: restarts=%d, want 4", n)
	}

	spec2 := childSpec("crash-after-respond", lg)
	spec2.PluginID = "org.example.crash-inside"
	spec2.Backoff.MaxRestarts = 2
	s2 := mustStart(t, spec2, nil)
	for i := 0; i < 3; i++ {
		pid := s2.Pid()
		_, _ = s2.Invoke(context.Background(), []byte(testReq))
		waitFor(t, fmt.Sprintf("outcome of crash %d inside the window", i), func() bool {
			st, _ := s2.State()
			return st == StateStopped || (st == StateRunning && s2.Pid() != 0 && s2.Pid() != pid)
		})
	}
	st, err := s2.State()
	if st != StateStopped {
		t.Fatalf("three crashes inside the window did not deactivate the plugin: %s", st)
	}
	var ceiling *RestartCeilingError
	if !errors.As(err, &ceiling) {
		t.Fatalf("the deactivation is typed: got %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestADeathNoticedByAFailedWriteStillCounts(t *testing.T) {
	capture, lg := newCapture()
	s := mustStart(t, childSpec("respond", lg), nil)
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	first := s.Pid()
	proc, err := os.FindProcess(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	waitFor(t, "the revived child to answer", func() bool {
		resp, ierr := s.Invoke(context.Background(), []byte(testReq))
		return ierr == nil && strings.Contains(string(resp), "answered")
	})
	if s.Pid() == first {
		t.Fatal("the child was not replaced")
	}
	if n := s.Restarts(); n != 1 {
		t.Fatalf("an outside kill is a crash and counts once: restarts=%d", n)
	}
	if !strings.Contains(capture.String(), "restart 1/") {
		t.Fatalf("the crash restart is on the record:\n%s", capture.String())
	}
}

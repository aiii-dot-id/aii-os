//go:build !windows

package supervisor

import (
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestACloseThatWinsARestartSpawnPublishesNoChild(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s, err := Start(Spec{
		PluginID: "raceclose-unix",
		Argv:     []string{"/bin/sleep", "300"},
		Backoff:  Backoff{Initial: 10 * time.Millisecond, Max: 10 * time.Millisecond, MaxRestarts: 5},
		VerifyArtifact: func() error {
			if calls.Add(1) >= 2 {
				entered <- struct{}{}
				<-release
			}
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := s.Pid()
	if pid == 0 {
		t.Fatal("no child pid")
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart never reached the verify seam")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	time.Sleep(50 * time.Millisecond) // Close is now waiting on the spawn
	close(release)
	select {
	case cerr := <-closed:
		if cerr != nil {
			t.Fatalf("close: %v", cerr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not observe the in-flight spawn retiring")
	}
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if p := s.Pid(); p != 0 {
			_, _ = s.KillChild()
			t.Fatalf("a closed supervisor published the raced spawn's child (pid %d)", p)
		}
		if st, _ := s.State(); st != StateStopped {
			t.Fatalf("a closed supervisor came back: state %v", st)
		}
	}
}

//go:build linux

package supervisor

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// .
// .
// .
func procChildren(t *testing.T) []string {
	t.Helper()
	self := strconv.Itoa(os.Getpid())
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		rest := strings.Fields(s[i+1:])
		if len(rest) < 2 || rest[1] != self {
			continue
		}
		out = append(out, rest[0]+" "+s[:i+1])
	}
	return out
}

func countMatching(list []string, substr string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

// .
// .
// .
// .
// .
func TestCloseDuringRestartDoesNotResurrect(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	image := &closeRaceImage{}
	spec := Spec{
		PluginID: "raceclose",
		Argv:     []string{"/bin/sleep", "300"},
		Backoff:  Backoff{Initial: 10 * time.Millisecond, Max: 10 * time.Millisecond, MaxRestarts: 5},
		Artifact: image,
		VerifyArtifact: func() error {
			if calls.Add(1) >= 2 {
				entered <- struct{}{}
				<-release
				if image.closed.Load() != 0 {
					return errors.New("verified image released during spawn")
				}
			}
			return nil
		},
	}
	s, err := Start(spec, nil)
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

	// .
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("restart never reached the verify seam")
	}

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case cerr := <-closed:
		t.Fatalf("Close acknowledged retirement while spawn was still in flight: %v", cerr)
	case <-time.After(50 * time.Millisecond):
	}
	if image.closed.Load() != 0 {
		t.Fatal("image binding closed before the in-flight spawn retired")
	}

	// .
	close(release)
	select {
	case cerr := <-closed:
		if cerr != nil {
			t.Fatalf("close: %v", cerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not observe the in-flight spawn retiring")
	}
	if image.closed.Load() != 1 {
		t.Fatal("image binding was not released once after retirement")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := s.State()
		if st != StateStopped {
			t.Fatalf("closed supervisor resurrected: state %v", st)
		}
		if s.Pid() != 0 {
			t.Fatalf("closed supervisor published a child: pid %d", s.Pid())
		}
		// .
		// .
		if countMatching(procChildren(t), "(sleep)") == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the raced child survived Close: %v", procChildren(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	// .
	time.Sleep(100 * time.Millisecond)
	if st, _ := s.State(); st != StateStopped {
		t.Fatalf("resurrected late: %v", st)
	}
}

type closeRaceImage struct{ closed atomic.Int32 }

func (i *closeRaceImage) Close() error { i.closed.Add(1); return nil }

// .
// .
// .
func TestSpawnRefusalReapsTheChild(t *testing.T) {
	baseline := countMatching(procChildren(t), "(sleep)")

	old := applyLimit
	applyLimit = func(pid int, bytes uint64) (string, error) {
		return "", errors.New("the kernel said no")
	}
	t.Cleanup(func() { applyLimit = old })

	spec := Spec{
		PluginID:      "refused",
		Argv:          []string{"/bin/sleep", "300"},
		RLimitASBytes: 1 << 20,
	}
	_, err := Start(spec, nil)
	var refused *SpawnRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want SpawnRefusedError, got %v", err)
	}

	// .
	deadline := time.Now().Add(3 * time.Second)
	for {
		if countMatching(procChildren(t), "(sleep)") <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("refused child not reaped: %v", procChildren(t))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// .
// .
// .
// .
func TestCloseContextCutsTheGraces(t *testing.T) {
	spec := Spec{
		PluginID: "gracecut",
		// .
		// .
		// .
		Argv: []string{"/bin/sh", "-c", `trap "" TERM; exec sleep 300`},
	}
	s, err := Start(spec, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.CloseContext(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("graces were not cut by the context: teardown took %v", elapsed)
	}
	if st, _ := s.State(); st != StateStopped {
		t.Fatalf("state after close: %v", st)
	}
}

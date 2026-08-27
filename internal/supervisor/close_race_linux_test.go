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

// procChildren scans /proc for children of THIS process, returning
// "state comm" lines. A zombie keeps its stat entry (state Z) until
// its parent waits — which is exactly what an unreaped refusal leaves.
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

// D04 (Sev 2026-08-26): Close during a restart's spawn returned while
// no child was published, and the in-flight spawn then published its
// child and promoted the supervisor back to running — a closed plugin
// resurrected. The VerifyArtifact seam holds the restart's spawn open
// deterministically while Close runs.
func TestCloseDuringRestartDoesNotResurrect(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	spec := Spec{
		PluginID: "raceclose",
		Argv:     []string{"/bin/sleep", "300"},
		Backoff:  Backoff{Initial: 10 * time.Millisecond, Max: 10 * time.Millisecond, MaxRestarts: 5},
		VerifyArtifact: func() error {
			if calls.Add(1) >= 2 {
				entered <- struct{}{}
				<-release
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

	// The restart's spawn is now parked inside VerifyArtifact.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("restart never reached the verify seam")
	}

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case cerr := <-closed:
		if cerr != nil {
			t.Fatalf("close: %v", cerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on the in-flight spawn")
	}

	// Let the doomed spawn run its full path.
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := s.State()
		if st != StateStopped {
			t.Fatalf("closed supervisor resurrected: state %v", st)
		}
		if s.Pid() != 0 {
			t.Fatalf("closed supervisor published a child: pid %d", s.Pid())
		}
		// The raced child must be killed AND reaped: no live and no
		// zombie sleep child of ours.
		if countMatching(procChildren(t), "(sleep)") == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the raced child survived Close: %v", procChildren(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	// And it stays stopped.
	time.Sleep(100 * time.Millisecond)
	if st, _ := s.State(); st != StateStopped {
		t.Fatalf("resurrected late: %v", st)
	}
}

// D18 (Sev 2026-08-26): a post-spawn refusal killed the child without
// reaping it — a zombie and three leaked pipes per refusal. The
// applyLimit seam makes the kernel-refusal deterministic.
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

	// Reaped means GONE — not live, and not a zombie either.
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

// D24 (Sev 2026-08-26): Close ran ten seconds of fixed graces however
// tight the caller's deadline was. A child that ignores both the EOF
// and SIGTERM proves the cut: with a 100ms context the whole teardown
// must reach SIGKILL far inside the old 5s of graces.
func TestCloseContextCutsTheGraces(t *testing.T) {
	spec := Spec{
		PluginID: "gracecut",
		// exec, so ONE process (with the inherited TERM-ignore) owns
		// the pipes: a grandchild holding them would add reap's own
		// pipe-drain grace and measure the D22 family, not D24.
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

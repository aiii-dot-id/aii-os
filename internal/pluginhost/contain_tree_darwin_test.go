//go:build darwin

package pluginhost

import (
	"context"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// wallWorkers waits for the two workers below root and returns each with
// the moment it started.
func wallWorkers(t *testing.T, root int) map[int]uint64 {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
		if err != nil {
			t.Fatal(err)
		}
		below := map[int]bool{root: true}
		found := map[int]uint64{}
		for grew := true; grew; {
			grew = false
			for i := range procs {
				p := &procs[i]
				pid := int(p.Proc.P_pid)
				if !below[pid] && below[int(p.Eproc.Ppid)] {
					below[pid], grew = true, true
					found[pid] = uint64(p.Proc.P_starttime.Sec)*1_000_000 + uint64(p.Proc.P_starttime.Usec)
				}
			}
		}
		if len(found) == 2 {
			return found
		}
	}
	t.Fatalf("the contained child %d never had its two workers", root)
	return nil
}

// stillRunning is the workers that run: the same pid started at the same
// moment, not waiting to be reaped.
func stillRunning(t *testing.T, workers map[int]uint64) []int {
	t.Helper()
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		t.Fatal(err)
	}
	var running []int
	for i := range procs {
		p := &procs[i]
		started := uint64(p.Proc.P_starttime.Sec)*1_000_000 + uint64(p.Proc.P_starttime.Usec)
		if s, ok := workers[int(p.Proc.P_pid)]; ok && s == started && p.Proc.P_stat != 5 { // 5: SZOMB, exited
			running = append(running, int(p.Proc.P_pid))
		}
	}
	return running
}

func startWalledStrays(t *testing.T) (*supervisor.Supervisor, map[int]uint64) {
	t.Helper()
	requireWall(t)
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "strays"}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	s, err := supervisor.Start(supervisor.Spec{
		PluginID:        "org.example.strays",
		Argv:            argv,
		ArgvContainment: wall,
		Env:             []string{"SEV_PLUGIN_SOCKET=stdio:"},
		ReadyMark:       "child-ready",
		Backoff:         supervisor.Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 3},
	}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	workers := wallWorkers(t, s.Pid())
	t.Cleanup(func() {
		for _, pid := range stillRunning(t, workers) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return s, workers
}

func TestOnMacOSAContainedChildsWorkersEndWhenItIsClosed(t *testing.T) {
	s, workers := startWalledStrays(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if running := stillRunning(t, workers); len(running) != 0 {
		t.Fatalf("the contained child was closed and its workers still run inside the wall: %v", running)
	}
}

func TestOnMacOSAContainedChildsWorkersEndWhenItDies(t *testing.T) {
	s, workers := startWalledStrays(t)
	first := s.Pid()
	if live, err := s.KillChild(); !live || err != nil {
		t.Fatalf("KillChild: live=%v err=%v", live, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for (s.Pid() == 0 || s.Pid() == first) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Pid() == 0 || s.Pid() == first {
		t.Fatal("the contained child was not revived")
	}
	if running := stillRunning(t, workers); len(running) != 0 {
		t.Fatalf("the contained child died and its workers still run inside the wall beside its successor: %v", running)
	}
	next := wallWorkers(t, s.Pid())
	t.Cleanup(func() {
		for _, pid := range stillRunning(t, next) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

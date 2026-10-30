//go:build darwin && !ios

package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// strays waits for the two workers below root and returns each with the
// moment it started, so a later look cannot take a reused pid for one.
func strays(t *testing.T, root int) map[int]uint64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		list := processList()
		if list == nil {
			t.Fatal("the kernel did not list the processes")
		}
		parent := map[int]int{}
		for pid, p := range list {
			parent[pid] = p.ppid
		}
		found := map[int]uint64{}
		for _, pid := range descendants(root, parent) {
			if pid != root && !list[pid].zombie {
				found[pid] = list[pid].started
			}
		}
		if len(found) == 2 {
			return found
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the carrier %d never had its two workers", root)
	return nil
}

// runningOf is the workers that still run: the same pid, started at the
// same moment, not waiting to be reaped.
func runningOf(t *testing.T, workers map[int]uint64) []int {
	t.Helper()
	list := processList()
	if list == nil {
		t.Fatal("the kernel did not list the processes")
	}
	var running []int
	for pid, started := range workers {
		if p, ok := list[pid]; ok && !p.zombie && p.started == started {
			running = append(running, pid)
		}
	}
	return running
}

// endStrays kills whatever a failed test left running.
func endStrays(t *testing.T, workers map[int]uint64) {
	t.Cleanup(func() {
		for _, pid := range runningOf(t, workers) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

func straySpec(next string) Spec {
	spec := childSpec("strays")
	spec.Argv = []string{fakechildBin, "strays", next}
	return spec
}

// THE RIG: the workers really do outlive their parent. Killed by its pid
// alone, outside the supervisor, the carrier leaves both running; without
// this every test below could pass on workers that end by themselves.
func TestTheStrayWorkersOutliveTheirCarrierWhenOnlyItsPidIsKilled(t *testing.T) {
	cmd := exec.Command(fakechildBin, "strays", "sleep")
	// Held open, so the carrier does not end at its input's end first.
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	workers := strays(t, cmd.Process.Pid)
	endStrays(t, workers)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(300 * time.Millisecond)
	if running := runningOf(t, workers); len(running) != 2 {
		t.Fatalf("rig: the workers must outlive a kill of their carrier's pid alone, still running: %v", running)
	}
}

func TestOnMacOSClosingANativeChildEndsItsWholeTree(t *testing.T) {
	s := mustStart(t, straySpec("respond"), nil)
	workers := strays(t, s.Pid())
	endStrays(t, workers)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if running := runningOf(t, workers); len(running) != 0 {
		t.Fatalf("Close returned with the child's workers still running: %v", running)
	}
}

func TestOnMacOSADeadlineKillEndsTheChildsWholeTree(t *testing.T) {
	s := mustStart(t, straySpec("sleep"), nil)
	first := s.Pid()
	workers := strays(t, first)
	endStrays(t, workers)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var terr *InvokeTimeoutError
	if _, err := s.Invoke(ctx, []byte(testReq)); !errors.As(err, &terr) {
		t.Fatalf("want InvokeTimeoutError, got %v", err)
	}
	waitFor(t, "revival after the deadline kill", func() bool {
		st, _ := s.State()
		return st == StateRunning && s.Pid() != 0 && s.Pid() != first
	})
	if running := runningOf(t, workers); len(running) != 0 {
		t.Fatalf("the killed child's workers still run beside its successor: %v", running)
	}
	endStrays(t, strays(t, s.Pid())) // the successor's, before the cleanup's Close
	if n := s.Restarts(); n != 0 {
		t.Fatalf("a host-requested kill was counted as a crash: restarts=%d", n)
	}
}

// A child that dies by itself is reaped with its tree: the workers seen at
// readiness, and every member of its group.
func TestOnMacOSAChildThatDiesTakesItsTreeWithItAtTheReap(t *testing.T) {
	s := mustStart(t, straySpec("respond"), nil)
	first := s.Pid()
	workers := strays(t, first)
	endStrays(t, workers)
	if live, err := s.KillChild(); !live || err != nil {
		t.Fatalf("KillChild: live=%v err=%v", live, err)
	}
	waitFor(t, "revival after the crash", func() bool {
		st, _ := s.State()
		return st == StateRunning && s.Pid() != 0 && s.Pid() != first
	})
	if running := runningOf(t, workers); len(running) != 0 {
		t.Fatalf("the dead child's workers still run beside its successor: %v", running)
	}
	endStrays(t, strays(t, s.Pid())) // the successor's, before the cleanup's Close
}

// A WORKER THAT OUTLIVES THE KILL IS RETIREMENT PENDING, NOT A CLEAN STOP.
// No process can be made to refuse SIGKILL on demand, so the seam spares
// one worker, which then really is a survivor.
func TestOnMacOSATreeThatOutlivesItsKillIsRetirementPending(t *testing.T) {
	savedGrace, savedKill := closeGraceKill, treeKill
	t.Cleanup(func() { closeGraceKill, treeKill = savedGrace, savedKill })
	capture := logsink.CaptureForTest(t)

	spec := straySpec("respond")
	s := mustStart(t, spec, nil)
	workers := strays(t, s.Pid())
	endStrays(t, workers)
	list := processList()
	spared := 0
	for pid := range workers {
		if list[pid].pgid == pid { // the worker in a group of its own
			spared = pid
		}
	}
	if spared == 0 {
		t.Fatal("rig: no worker in a group of its own")
	}
	closeGraceKill = 300 * time.Millisecond
	treeKill = func(pid int, sig syscall.Signal) error {
		if pid == spared {
			return nil
		}
		return savedKill(pid, sig)
	}

	err := s.Close()
	var cce *ContainmentCleanupError
	if !errors.As(err, &cce) {
		t.Fatalf("a tree that outlived its kill was not reported as pending retirement: %v", err)
	}
	if want := fmt.Sprintf("(pids [%d]); retirement pending", spared); !strings.Contains(err.Error(), want) {
		t.Fatalf("the pending retirement does not name its survivor %q: %v", want, err)
	}
	if running := runningOf(t, workers); len(running) != 1 || running[0] != spared {
		t.Fatalf("rig: only the spared worker should run, running: %v", running)
	}
	if st, _ := s.State(); st != StateStopped {
		t.Fatalf("state %v after a pending retirement, want stopped", st)
	}
	if line := "plugins.error: plugin " + spec.PluginID + ": supervisor: " + spec.PluginID + ": containment cleanup: 1 process(es) of the plugin's process tree still running"; !strings.Contains(capture.String(), line) {
		t.Fatalf("the survivor was not told to the operator (want %q):\n%s", line, capture.String())
	}
}

// TestHelperHostOfATree is a plugin host for the test below, run as its own
// process: it starts the carrier, says its pid, and waits to be killed. Run
// any other way it does nothing.
func TestHelperHostOfATree(t *testing.T) {
	if os.Getenv(treeHostEnv) == "" {
		return
	}
	s, err := Start(straySpec("respond"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	fmt.Printf("carrier=%d\n", s.Pid())
	select {}
}

func TestOnMacOSATreeEndsWhenItsHostIsKilled(t *testing.T) {
	host := exec.Command(os.Args[0], "-test.run=^TestHelperHostOfATree$", "-test.count=1")
	host.Env = append(os.Environ(), treeHostEnv+"="+fakechildBin)
	out, err := host.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Process.Kill(); _ = host.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the host never said its carrier: %v", err)
	}
	carrier, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), "carrier="))
	if err != nil {
		t.Fatalf("the host said %q", line)
	}
	workers := strays(t, carrier)
	endStrays(t, workers)
	list := processList()
	inGroup := map[int]uint64{carrier: list[carrier].started}
	for pid, started := range workers {
		if list[pid].pgid != pid {
			inGroup[pid] = started
		}
	}
	endStrays(t, inGroup)
	if len(inGroup) != 2 {
		t.Fatalf("rig: want the carrier and one worker in its group, have %v", inGroup)
	}

	_ = host.Process.Kill()
	_ = host.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for len(runningOf(t, inGroup)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if running := runningOf(t, inGroup); len(running) != 0 {
		t.Fatalf("the carrier's group still runs 5s after its host was killed: %v", running)
	}
}

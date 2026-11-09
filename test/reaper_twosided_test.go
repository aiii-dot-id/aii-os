//go:build !windows

package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTheReaperFindsItsOwnAndLeavesEverythingElseAlone is the two-sided
// evidence for reapByMark: a process carrying this subtest's state
// directory is ended, and a process that is unmarked, owned by a different
// state directory, or merely *named* like the fixture is left running.
//
// Processes left running by earlier runs are what this exists to
// prevent. They are not this test's to stop, and it does not touch them:
// every process it starts is started here, marked here, and killed here.
func TestTheReaperFindsItsOwnAndLeavesEverythingElseAlone(t *testing.T) {
	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("no /proc: this reaper is Linux only")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid: cannot give the fixture its own process group")
	}

	// The state directories are built explicitly so that one is a strict
	// prefix of another in each direction. t.TempDir() gives independent
	// random paths, which cannot collide as prefixes, so the whole-entry
	// match was only ever argued from source and never run against the
	// case it exists to reject.
	base := t.TempDir()
	mine := filepath.Join(base, "state")
	shorter := filepath.Join(base, "sta")         // a prefix of mine
	longer := filepath.Join(base, "state-longer") // mine is a prefix of this
	for _, d := range []string{mine, shorter, longer} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("make state dir %s: %v", d, err)
		}
	}
	other := t.TempDir()

	// start launches a marked sleeper in its own process group and returns
	// its pid. It writes nothing into FIXTURE_STATE: the point is that the
	// mark in the environment is the only thing that identifies it, which
	// is exactly the case where a fixture's own bookkeeping never got
	// written.
	start := func(t *testing.T, mark string) int {
		t.Helper()
		cmd := exec.Command("setsid", "sleep", "120")
		if mark != "" {
			cmd.Env = append(os.Environ(), mark)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start fixture: %v", err)
		}
		pid := cmd.Process.Pid
		t.Cleanup(func() {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
			_ = cmd.Wait()
		})
		return pid
	}

	// alive asks /proc, not the wait status: a zombie has exited, and the
	// question here is whether the process still runs.
	alive := func(pid int) bool {
		st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		body := string(st)
		i := bytes.LastIndexByte([]byte(body), ')')
		if i < 0 || i+1 >= len(body) {
			return false
		}
		return !strings.HasPrefix(strings.TrimSpace(body[i+1:]), "Z")
	}

	owned := start(t, "FIXTURE_STATE="+mine)
	foreign := start(t, "FIXTURE_STATE="+other)
	unmarked := start(t, "")
	// A state directory that is a PREFIX of this subtest's: a reaper that
	// matched with strings.HasPrefix would take it, and so would one that
	// swept by process name.
	prefixShorter := start(t, "FIXTURE_STATE="+shorter)
	// And one that this subtest's state directory is a prefix OF.
	prefixLonger := start(t, "FIXTURE_STATE="+longer)
	// A process whose *name* matches the fixture but whose environment says
	// nothing about this state directory. A broad process-name match would
	// kill this one; the mark must not.
	colliding := exec.Command("setsid", "sleep", "120")
	colliding.Env = append(os.Environ(), "FIXTURE_MODE=unresponsive", "FIXTURE_DETACH=1")
	if err := colliding.Start(); err != nil {
		t.Fatalf("start colliding fixture: %v", err)
	}
	collidingPid := colliding.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-collidingPid, syscall.SIGKILL)
		_ = syscall.Kill(collidingPid, syscall.SIGKILL)
		_ = colliding.Wait()
	})

	// Everything must be running before the reaper is asked, or the test
	// would pass by having started nothing.
	for name, pid := range map[string]int{"owned": owned, "foreign": foreign, "unmarked": unmarked, "colliding": collidingPid, "prefix-shorter": prefixShorter, "prefix-longer": prefixLonger} {
		if !alive(pid) {
			t.Fatalf("%s (pid %d) was not running before the reaper was asked", name, pid)
		}
	}

	killed, survivors := reapByMark(mine)

	// Side one: the owned process is ended, and the reaper says it asked.
	deadline := time.Now().Add(5 * time.Second)
	for alive(owned) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(owned) {
		t.Errorf("the reaper left its own marked process (pid %d) running", owned)
	}
	if len(killed) == 0 {
		t.Errorf("the reaper ended nothing and reported nothing; the owned process is gone only by accident")
	}

	// Side two: everything else is untouched. A reaper that sweeps by
	// process name, by pid proximity, or by "anything that looks like the
	// fixture" would fail here.
	for name, pid := range map[string]int{"foreign": foreign, "unmarked": unmarked, "colliding": collidingPid, "prefix-shorter": prefixShorter, "prefix-longer": prefixLonger} {
		if !alive(pid) {
			t.Errorf("the reaper killed %s (pid %d), which this state directory does not own", name, pid)
		}
	}

	// And it is bounded: asking twice must terminate and must not report the
	// same process again, because it is already gone.
	done := make(chan []int, 1)
	go func() {
		second, _ := reapByMark(mine)
		done <- second
	}()
	select {
	case second := <-done:
		for _, pid := range second {
			if pid == owned {
				t.Errorf("the second sweep reported pid %d again, which had already exited", owned)
			}
		}
	case <-time.After(10 * time.Second):
		t.Errorf("the second sweep did not terminate within 10s")
	}

	// The observation is what makes the kill an established fact rather
	// than a request: nothing this sweep owned may be reported alive.
	if len(survivors) > 0 {
		t.Errorf("the reaper asked but these marked processes are still running: %v", survivors)
	}
}

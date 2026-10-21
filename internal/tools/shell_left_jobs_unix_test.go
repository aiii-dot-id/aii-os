//go:build !windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// leftShell is a shell whose left jobs do not outlive the test.
func leftShell(t *testing.T) *ShellTool {
	t.Helper()
	st := &ShellTool{timeout: 20 * time.Second, sandbox: t.TempDir()}
	t.Cleanup(func() { st.left.end(0) })
	return st
}

func run(t *testing.T, st *ShellTool, command string) Result {
	t.Helper()
	res, err := st.Execute(context.Background(), map[string]any{"command": command})
	if err != nil || res.Error != "" {
		t.Fatalf("%s: err=%v error=%q", command, err, res.Error)
	}
	return res
}

func kept(st *ShellTool) []leftJob {
	st.left.mu.Lock()
	defer st.left.mu.Unlock()
	return append([]leftJob(nil), st.left.jobs...)
}

// groupLive: the group has a member that has not exited.
func groupLive(pgid int) bool { return len(processGroups()[pgid]) > 0 }

func waitGone(t *testing.T, pgid int, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); groupLive(pgid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("process group %d is still running after %s", pgid, within)
		}
	}
}

// A JOB THE COMMAND LEFT RUNNING IS SAID AND KEPT.
// Before, the shell returned and nothing knew of the job: one
// that held the output was told as an exit status with a caveat, one that
// had sent its output elsewhere was not told at all, and neither was
// anybody's to end.
func TestAJobTheCommandLeftRunningIsSaidAndKept(t *testing.T) {
	said := "[" + shellLeftJob + "]"
	for _, c := range []struct{ name, command, want string }{
		{"holding the output", `sleep 30 &`,
			"[exit status 0; a process the command left running still held its output after the exit — output from here on is not captured; no output]\n" + said},
		{"its output sent elsewhere", `sleep 30 >/dev/null 2>&1 &`, "[" + shellLeftJob + "; no output]"},
		{"after output of the command's own", `echo started; nohup sleep 30 >/dev/null 2>&1 & disown`, "started\n" + said},
		{"from a subshell, with a failing exit", `(sleep 30 >/dev/null 2>&1 &); exit 3`, "[exit status 3; no output]\n" + said},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := leftShell(t)
			res := run(t, st, c.command)
			if res.Output != c.want {
				t.Fatalf("the result of %q:\n got %q\nwant %q", c.command, res.Output, c.want)
			}
			jobs := kept(st)
			if len(jobs) != 1 {
				t.Fatalf("one job was left and %d are kept", len(jobs))
			}
			if !groupLive(jobs[0].pgid) {
				t.Fatalf("the job that was said to be running is not: group %d", jobs[0].pgid)
			}
		})
	}
}

// WITH NOTHING LEFT THE RESULT IS WHAT IT WAS, TO THE BYTE, and nothing
// is kept: a job that ended before its shell did, or while the call was
// still reading its output, was never left.
func TestACommandThatLeftNothingSaysWhatItAlwaysSaid(t *testing.T) {
	st := leftShell(t)
	for _, c := range []struct{ command, want string }{
		{`echo hi`, "hi\n"},
		{`true`, ""},
		{`exit 3`, "[exit status 3; no output]"},
		{`echo a; exit 2`, "a\n[exit status 2]"},
		{`sleep 0.1 & wait; echo done`, "done\n"},
		{`sleep 0.2 & echo started`, "started\n"},
	} {
		if res := run(t, st, c.command); res.Output != c.want {
			t.Fatalf("the result of %q:\n got %q\nwant %q", c.command, res.Output, c.want)
		}
		if jobs := kept(st); len(jobs) != 0 {
			t.Fatalf("%q left nothing and %d jobs are kept", c.command, len(jobs))
		}
	}
}

// THE HOST ENDS WHAT IS KEPT: asked first, and killed when it does not
// go. This is what SAFE and a stop call (internal/app holds those two
// ends), through the registry every caller of the shell shares.
func TestEndingTheLeftJobsAsksThemToStopAndKillsWhatDoesNot(t *testing.T) {
	reg := NewRegistry(t.TempDir(), nil, Timeouts{})
	if reg.shell == nil {
		t.Skip("this host has no shell")
	}
	t.Cleanup(func() { reg.EndLeftJobs(0) })
	for _, command := range []string{
		`sleep 30 >/dev/null 2>&1 &`,
		`(trap '' TERM; sleep 30) >/dev/null 2>&1 &`, // ignores being asked
	} {
		res, err := reg.Execute(context.Background(), "shell", map[string]any{"command": command})
		if err != nil || res.Failed() {
			t.Fatalf("%s: %v %+v", command, err, res)
		}
	}
	jobs := kept(reg.shell)
	if len(jobs) != 2 {
		t.Fatalf("two jobs were left and %d are kept", len(jobs))
	}
	const wait = 400 * time.Millisecond
	began := time.Now()
	ended, err := reg.EndLeftJobs(wait)
	took := time.Since(began)
	if ended != 2 || err != nil {
		t.Fatalf("ended %d of 2, %v", ended, err)
	}
	if took < wait || took > wait+2*time.Second {
		t.Fatalf("the ending took %s: the job that ignores being asked has %s, and is then killed", took, wait)
	}
	waitGone(t, jobs[0].pgid, time.Second) // it went when asked
	waitGone(t, jobs[1].pgid, 2*time.Second)
	if left := kept(reg.shell); len(left) != 0 {
		t.Fatalf("%d jobs are still kept after the ending", len(left))
	}
}

// AN EVENT BETWEEN THE ENDING AND A CALL'S END. A command already
// running when SAFE is entered, or when the host begins to stop, runs to
// its end. The job it leaves then would arrive in a list nobody reads
// again, so it is killed as it is left, and the result does not say a job
// is running.
func TestAJobLeftAfterTheEndingIsKilledAsItIsLeft(t *testing.T) {
	st := leftShell(t)
	if ended, err := st.left.end(time.Second); ended != 0 || err != nil {
		t.Fatalf("nothing was kept: ended %d, %v", ended, err)
	}
	res := run(t, st, `echo $$; sleep 30 >/dev/null 2>&1 &`)
	var pgid int
	if _, err := fmt.Sscanf(res.Output, "%d\n", &pgid); err != nil || res.Output != fmt.Sprintf("%d\n", pgid) {
		t.Fatalf("the result is the shell's pid and nothing else, got %q (%v)", res.Output, err)
	}
	waitGone(t, pgid, 2*time.Second)
	if jobs := kept(st); len(jobs) != 0 {
		t.Fatalf("%d jobs are kept after the ending", len(jobs))
	}
}

// A JOB THAT ENDS BY ITSELF IS FORGOTTEN at the next call's end, so the
// list is the jobs that are running and never the jobs there have been.
func TestAHundredJobsThatEndedLeaveNoneKept(t *testing.T) {
	st := leftShell(t)
	for i := 0; i < 100; i++ {
		// Long enough to outlive the call's end on a loaded host: at 0.5 s
		// a gate under load saw the job end before the call looked.
		if res := run(t, st, `sleep 3 >/dev/null 2>&1 &`); res.Output != "["+shellLeftJob+"; no output]" {
			t.Fatalf("job %d was left running and the result says %q", i, res.Output)
		}
	}
	if len(kept(st)) == 0 {
		t.Fatal("jobs are running and none is kept")
	}
	// Each call's end forgets the jobs that have ended; poll with a call
	// until all hundred have, rather than guess how long a loaded host takes.
	deadline := time.Now().Add(20 * time.Second)
	for {
		time.Sleep(200 * time.Millisecond)
		run(t, st, `true`)
		jobs := kept(st)
		if len(jobs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a hundred jobs have ended and %d are still kept", len(jobs))
		}
	}
}

// A GROUP ID THAT HAS PASSED TO ANOTHER GROUP IS NOT SIGNALLED. A kept
// job is its group's id and the processes seen in it, each with its start
// time. Here the id is held by a live group, as a reused one would be,
// and the processes the list remembers under it started at another time:
// the ending leaves that group alone. With the processes it really holds,
// the same ending ends it.
func TestAGroupWhoseIdHasPassedToAnotherIsNotSignalled(t *testing.T) {
	st := leftShell(t)
	run(t, st, `sleep 30 >/dev/null 2>&1 &`)
	jobs := kept(st)
	if len(jobs) != 1 {
		t.Fatalf("one job was left and %d are kept", len(jobs))
	}
	theirs := jobs[0]
	remembered := map[int]uint64{theirs.pgid: 1} // the shell that once led a group of this id
	for pid, started := range theirs.members {
		remembered[pid] = started + 1 // the same pid, another process
	}
	stale := &leftJobs{jobs: []leftJob{{pgid: theirs.pgid, members: remembered}}}
	if ended, err := stale.end(200 * time.Millisecond); ended != 0 || err != nil {
		t.Fatalf("a group that is not the remembered job's was ended: %d, %v", ended, err)
	}
	time.Sleep(100 * time.Millisecond)
	if !groupLive(theirs.pgid) {
		t.Fatal("the group that now holds the id was signalled")
	}
	if ended, err := st.left.end(time.Second); ended != 1 || err != nil {
		t.Fatalf("the job's own list ends it: %d, %v", ended, err)
	}
	waitGone(t, theirs.pgid, time.Second)
}

// The guard itself: a job is known by a process seen in its group before,
// the same pid started at the same time, and by nothing less.
func TestALeftJobIsKnownByAProcessSeenInItBefore(t *testing.T) {
	job := leftJob{pgid: 100, members: map[int]uint64{101: 5000, 102: 5001}}
	for _, c := range []struct {
		name string
		now  map[int]uint64
		want bool
	}{
		{"one of them is still there", map[int]uint64{102: 5001, 300: 9000}, true},
		{"the pid is there and started at another time", map[int]uint64{101: 7000}, false},
		{"only processes never seen", map[int]uint64{100: 7000, 300: 9000}, false},
		{"the group is empty", nil, false},
	} {
		if got := job.in(c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	now := map[int]map[int]uint64{100: {102: 5001, 300: 9000}, 200: {101: 5000}}
	still := running([]leftJob{job, {pgid: 200, members: map[int]uint64{201: 1}}}, now)
	if len(still) != 1 || still[0].pgid != 100 || len(still[0].members) != 2 || still[0].members[300] != 9000 {
		t.Fatalf("the job that is running, with the members it has now: got %+v", still)
	}
}

// THE HOST'S OWN GROUP IS NEVER SIGNALLED, nor the two ids kill reads as
// "my group" and "everything I may signal".
func TestTheHostsOwnGroupIsNeverSignalled(t *testing.T) {
	for _, pgid := range []int{syscall.Getpgrp(), 1, 0, -7} {
		if err := signalGroup(pgid, syscall.Signal(0)); !errors.Is(err, errNotAJob) {
			t.Errorf("process group %d: %v, want the refusal", pgid, err)
		}
	}
}

// The reading itself, on this host: this process is in its own group.
func TestTheReadingOfProcessGroupsFindsThisProcess(t *testing.T) {
	if _, ok := processGroups()[syscall.Getpgrp()][os.Getpid()]; !ok {
		t.Fatal("this process is not in the reading of its own group")
	}
}

// A READING OF THE PROCESS TABLE THAT FAILS IS NOT AN EMPTY TABLE. Taken
// for one, it forgot every kept job, and the ending found none to end, so
// they ran on through SAFE and the stop. While the
// table cannot be read, a call keeps what was kept, and the ending keeps
// the jobs and says it could not end them; once it reads again, they end.
func TestAProcessTableThatCannotBeReadForgetsNoJob(t *testing.T) {
	st := leftShell(t)
	if res := run(t, st, `sleep 30 >/dev/null 2>&1 &`); !strings.Contains(res.Output, shellLeftJob) {
		t.Fatalf("fixture: no job was left: %q", res.Output)
	}
	pgid := kept(st)[0].pgid
	real := groupsReader
	t.Cleanup(func() { groupsReader = real })
	groupsReader = func() map[int]map[int]uint64 { return nil }

	run(t, st, `true`)
	if len(kept(st)) != 1 {
		t.Fatalf("a call that could not read the table forgot the kept job: %d kept", len(kept(st)))
	}
	if n, err := st.left.end(time.Second); n != 0 || !errors.Is(err, errNoProcessTable) {
		t.Fatalf("an ending that could not read the table must say so and end none: %d, %v", n, err)
	}
	if len(kept(st)) != 1 || !groupLive(pgid) {
		t.Fatal("the job is no longer kept, or was signalled unseen")
	}
	groupsReader = real
	if n, err := st.left.end(2 * time.Second); n != 1 || err != nil {
		t.Fatalf("read again, the ending ends the job: %d, %v", n, err)
	}
	waitGone(t, pgid, 5*time.Second)
}

//go:build !windows

package app

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// leaveAJob gives the identity a tool registry and runs one command
// through it that exits leaving `sleep 30` behind. It returns the job's
// process group, which is running.
func leaveAJob(t *testing.T, a *App) int {
	t.Helper()
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	// Whatever the test finds, the job does not outlive it. By the
	// registry's own guarded ending, never by the bare number: once the
	// job has ended that number may be anybody's.
	t.Cleanup(func() { a.toolReg.EndLeftJobs(0) })
	res, err := a.toolReg.Execute(context.Background(), "shell", map[string]any{"command": `echo $$; sleep 30 >/dev/null 2>&1 &`})
	if err != nil || res.Failed() {
		t.Fatalf("the command that leaves a job: %v %+v", err, res)
	}
	var pgid int
	if _, err := fmt.Sscanf(res.Output, "%d\n", &pgid); err != nil {
		t.Fatalf("the shell's own pid is the first line of %q: %v", res.Output, err)
	}
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("the job the command left is not running: %v", err)
	}
	return pgid
}

func jobEnded(t *testing.T, pgid int, within time.Duration, when string) {
	t.Helper()
	for deadline := time.Now().Add(within); syscall.Kill(-pgid, 0) != syscall.ESRCH; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the job the shell left running is still running %s after %s", within, when)
		}
	}
}

// SAFE IS THE WHOLE IDENTITY'S, WHAT IT LEFT RUNNING INCLUDED.
// SAFE refuses every new command. A job an earlier command
// had left went on with the account's reach, writing in the home while
// the page said only reading continues: "no process.run" was true of new
// calls and false of that one. Entering SAFE now ends it.
func TestEnteringSafeEndsWhatTheShellLeftRunning(t *testing.T) {
	a := newBareApp(t, &Config{})
	pgid := leaveAJob(t, a)
	a.enterSafe("test: the record cannot be trusted")
	defer a.resetModeForTest()
	jobEnded(t, pgid, 5*time.Second, "SAFE was entered")
}

// AND A STOP OF THE IDENTITY ENDS IT, before the stop returns. Under a
// service manager the manager did that; a host run as a plain process
// left the job running after it.
func TestAStopOfTheIdentityEndsWhatTheShellLeftRunning(t *testing.T) {
	a := New(defaultConfig())
	pgid := leaveAJob(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("the stop: %v", err)
	}
	// It was asked and seen gone before Shutdown returned; the wait here
	// is for whoever reaps it.
	jobEnded(t, pgid, time.Second, "the stop returned")
}

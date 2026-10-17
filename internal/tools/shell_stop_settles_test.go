//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A STOP SETTLES. A command stopped with its turn leaves nothing running:
// the call returns at once, the processes the command started and the
// ones those started are gone, and the next call runs. The ceiling's kill
// has had this test since it was made to reach the tree
// (TestShellTimeoutKillsProcessTree); the stop takes the same road and
// nothing asked it. A stop that is only reported, with the work still
// running behind it, is the cancellation an operator cannot trust.
func TestACommandStoppedWithItsTurnLeavesNoProcessBehind(t *testing.T) {
	home := t.TempDir()
	st := &ShellTool{timeout: 60 * time.Second, sandbox: home}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		for ctx.Err() == nil {
			// The marker is written after both pid lines.
			if _, err := os.Stat(filepath.Join(home, "started")); err == nil {
				stop()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	began := time.Now()
	res, err := st.Execute(ctx, map[string]any{
		"command": `sleep 120 & echo child:$!; (sleep 120 & echo grandchild:$!; touch started; wait) & wait`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if held := time.Since(began); held > 10*time.Second {
		t.Fatalf("the call held for %s after the stop", held)
	}
	if !res.Interrupted || !strings.Contains(res.Output, "the turn was stopped") {
		t.Fatalf("the stop is not what the result says: %+v", res)
	}
	found := 0
	for _, line := range strings.Split(res.Output, "\n") {
		who, pidText, ok := strings.Cut(line, ":")
		if !ok || who != "child" && who != "grandchild" {
			continue
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(pidText))
		if perr != nil {
			t.Fatalf("could not read a pid from %q", line)
		}
		found++
		// Signal 0 asks whether the process exists; ESRCH is the passing
		// answer. One grace beat for the kernel to reap.
		deadline := time.Now().Add(3 * time.Second)
		for {
			kerr := syscall.Kill(pid, 0)
			if kerr == syscall.ESRCH {
				break
			}
			if time.Now().After(deadline) {
				// Not Fatalf: the other one must be looked for too, and
				// neither may be left running in the suite.
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Errorf("the %s (%d) outlived the stop (kill(0) => %v)", who, pid, kerr)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if found != 2 {
		t.Fatalf("the output must name the child and its child: %q", res.Output)
	}
	next, err := st.Execute(context.Background(), map[string]any{"command": "echo next"})
	if err != nil || next.Failed() || next.Output != "next\n" {
		t.Fatalf("the call after a stop: %+v (%v)", next, err)
	}
}

//go:build !windows

package tools

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// .
// .
// .
// .
func TestShellTimeoutKillsProcessTree(t *testing.T) {
	st := &ShellTool{timeout: 400 * time.Millisecond, sandbox: t.TempDir()}

	start := time.Now()
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `sleep 60 & echo child:$!; wait`,
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("Run held for %s — the orphan still owns the ceiling", elapsed)
	}
	if !strings.Contains(res.Output, "timed out after") {
		t.Fatalf("expected the timeout report, got: %q", res.Output)
	}

	// .
	for _, line := range strings.Split(res.Output, "\n") {
		if strings.HasPrefix(line, "child:") {
			pid, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "child:")))
			if perr != nil {
				t.Fatalf("could not parse child pid from %q", line)
			}
			// .
			// .
			deadline := time.Now().Add(2 * time.Second)
			for {
				err := syscall.Kill(pid, 0)
				if err == syscall.ESRCH {
					return
				}
				if time.Now().After(deadline) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("child %d survived the ceiling (kill(0) => %v)", pid, err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	t.Fatalf("child pid line missing from output: %q", res.Output)
}

// .
// .
func TestShellDialectIsDeclaredUnix(t *testing.T) {
	st := &ShellTool{}
	if !strings.Contains(st.Description(), "bash") {
		t.Fatalf("unix shell tool does not declare its dialect: %q", st.Description())
	}
}

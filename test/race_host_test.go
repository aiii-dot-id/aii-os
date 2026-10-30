package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// raceRefusal is what the race runtime says on a host it cannot run on: on
// arm64 it needs a 48-bit address space, and a kernel built for fewer
// refuses every race build at start (Raspberry Pi OS on a Pi 5, 47 bits,
// 2026-10-28: "FATAL: ThreadSanitizer: unsupported VMA range").
const raceRefusal = "ThreadSanitizer: unsupported VMA range"

var raceProbe struct {
	once sync.Once
	out  string
	err  error
}

// skipWhereRaceCannotRun skips a test that runs a race build where the host
// refuses one, and only there: a race build that fails for any other reason
// fails the test.
func skipWhereRaceCannotRun(t *testing.T, goBin string) {
	t.Helper()
	raceProbe.once.Do(func() {
		dir, err := os.MkdirTemp("", "aii-race-probe")
		if err != nil {
			raceProbe.err = err
			return
		}
		defer os.RemoveAll(dir)
		main := filepath.Join(dir, "main.go")
		if raceProbe.err = os.WriteFile(main, []byte("package main\n\nfunc main() {}\n"), 0o600); raceProbe.err != nil {
			return
		}
		cmd := exec.Command(goBin, "run", "-race", main)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		raceProbe.out, raceProbe.err = string(out), err
	})
	switch {
	case raceProbe.err == nil:
	case strings.Contains(raceProbe.out, raceRefusal):
		t.Skipf("this host's kernel cannot run a race build: %s", raceRefusal)
	default:
		t.Fatalf("a race build of an empty program did not run: %v\n%s", raceProbe.err, raceProbe.out)
	}
}

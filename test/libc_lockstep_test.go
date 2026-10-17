package test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// guard:tree
func TestLibcIsTheOneSQLiteWasBuiltAgainst(t *testing.T) {
	root := repoRoot(t)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(goToolPath(), args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	sqlite := run("list", "-m", "-f", "{{.Version}}", "modernc.org/sqlite")
	libc := run("list", "-m", "-f", "{{.Version}}", "modernc.org/libc")
	if err := libcLockstep(sqlite, libc, run("mod", "graph")); err != nil {
		t.Error(err)
	}
}

// The rule refuses a split and a graph it cannot read, and passes a
// matched pair.
//
// guard:tree
func TestTheLibcLockstepGuardRefusesWhatItIsFor(t *testing.T) {
	graph := "example.com/m modernc.org/sqlite@v1.56.0\n" +
		"modernc.org/sqlite@v1.56.0 modernc.org/libc@v1.74.4\n" +
		"modernc.org/sqlite@v1.55.0 modernc.org/libc@v1.70.0\n"
	if err := libcLockstep("v1.56.0", "v1.74.4", graph); err != nil {
		t.Errorf("a matched pair was refused: %v", err)
	}
	if err := libcLockstep("v1.56.0", "v1.75.0", graph); err == nil {
		t.Error("a libc newer than the one sqlite requires passed")
	}
	if err := libcLockstep("v1.57.0", "v1.74.4", graph); err == nil {
		t.Error("a graph with no requirement of the selected sqlite passed")
	}
}

// libcLockstep reports a selected libc other than the one the selected
// sqlite requires in graph (`go mod graph` output).
func libcLockstep(sqlite, libc, graph string) error {
	for _, line := range strings.Split(graph, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != "modernc.org/sqlite@"+sqlite {
			continue
		}
		if want, ok := strings.CutPrefix(f[1], "modernc.org/libc@"); ok {
			if libc != want {
				return fmt.Errorf("modernc.org/libc is %s, but modernc.org/sqlite %s was built against %s — move them together (go.mod), never one alone", libc, sqlite, want)
			}
			return nil
		}
	}
	return fmt.Errorf("go mod graph shows no libc requirement of modernc.org/sqlite@%s — the guard is not reading the graph", sqlite)
}

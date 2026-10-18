package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guards_revert_test.go — the runner that is run before a commit asks what
// the branch would take away.
//
// The gate asks it too, and later: by then the commit exists, has been
// rebased, and has cost a gate. The fault is made at the moment of staging
// from a directory older than main, so the question belongs in the thing
// an author runs at that moment. This drives the shipped runner, with the
// shipped check beside it, in a repository of the shape that landed.

// guardsIn runs test/guards.sh as it ships inside dir, which holds one
// marked test of its own so the runner reaches its verdict rather than
// stopping at "nothing carries the marker".
func guardsIn(t *testing.T, dir, base string) (int, string) {
	t.Helper()
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no go on the path and AII_GO unset: the runner cannot run its guards here")
		}
	}
	for _, name := range []string{"guards.sh", "no_silent_revert.sh", "toolchain.sh"} {
		src, err := os.ReadFile(filepath.Join(repoRoot(t), "test", name))
		if err != nil {
			t.Fatal(err)
		}
		write(t, dir, filepath.Join("test", name), string(src))
		if err := os.Chmod(filepath.Join(dir, "test", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, dir, "go.mod", "module stale\n\ngo 1.21\n")
	write(t, dir, "held_test.go", "package stale\n\nimport \"testing\"\n\n// guard:tree\nfunc TestHeld(t *testing.T) {}\n")

	cmd := exec.Command("sh", filepath.Join(dir, "test", "guards.sh"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GATE_BASE="+base, "AII_GO="+goBin, "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the guards: %v\n%s", err, out)
	}
	return code, string(out)
}

// A COMMIT STAGED FROM A STALE DIRECTORY IS REFUSED BEFORE IT IS OFFERED
// TO A GATE, and the refusal names what was taken.
func TestTheGuardsRefuseABranchThatRemovesWorkItNeverWrote(t *testing.T) {
	dir, base := staleRepo(t)
	code, out := guardsIn(t, dir, base)
	if code != 1 || !strings.Contains(out, "GUARDS_FAIL") {
		t.Fatalf("the guards passed a branch that deletes a file it never wrote (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "guards: FAIL revert check") || !strings.Contains(out, "internal/theirs.go") {
		t.Errorf("the refusal does not say which check refused, or what was taken:\n%s", out)
	}
	if !strings.Contains(out, "guards: ok   . (TestHeld)") {
		t.Errorf("the rig's own guard did not run, so the verdict above is not the revert check's alone:\n%s", out)
	}
}

// AND THE SAME BRANCH, ONCE IT SAYS SO, PASSES: the runner adds nothing to
// the check's rule, it only asks it sooner.
func TestTheGuardsPassABranchThatNamesWhatItRemoves(t *testing.T) {
	dir, base := staleRepo(t)
	git(t, dir, "commit", "-q", "--amend", "-m", "mine, and internal/theirs.go is removed because it is dead")
	code, out := guardsIn(t, dir, base)
	if code != 0 || !strings.Contains(out, "GUARDS_PASS") {
		t.Fatalf("the guards refused a removal the commit message names (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "guards: ok   revert check") {
		t.Errorf("the runner did not report the revert check at all:\n%s", out)
	}
}

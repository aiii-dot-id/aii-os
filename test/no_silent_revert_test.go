package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// runGuard returns the guard's exit code and output for HEAD against base.
func runGuard(t *testing.T, dir, base string) (int, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "test", "no_silent_revert.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GATE_BASE="+base)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the guard: %v\n%s", err, out)
	}
	return code, string(out)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func staleRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "shared.txt", "one\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	cut := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	// Their landing, on main.
	write(t, dir, "internal/theirs.go", strings.Repeat("// theirs\n", 60))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "they land a file")
	base = strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	// A working directory still at the older base: no theirs.go in it.
	git(t, dir, "checkout", "-q", "-b", "mine", cut)
	write(t, dir, "mine.txt", "my work\n")

	// HEAD moves onto main; the directory is left as it was.
	git(t, dir, "reset", "-q", "--mixed", "main")

	// And now the fatal convenience.
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "my own unrelated change")
	return dir, base
}

// A LANDING THAT CARRIES SOMEONE ELSE'S FILE AWAY IS REFUSED.
func TestTheGuardRefusesALandingThatRemovesWorkItNeverWrote(t *testing.T) {
	dir, base := staleRepo(t)
	code, out := runGuard(t, dir, base)
	if code == 0 {
		t.Fatalf("the guard passed a landing that deleted a file it never wrote:\n%s", out)
	}
	if !strings.Contains(out, "internal/theirs.go") {
		t.Errorf("the refusal does not name the file that was taken away:\n%s", out)
	}
	if !strings.Contains(out, "git add -A") {
		t.Errorf("the refusal does not say what causes this, so the next reader has to guess:\n%s", out)
	}
}

// A REMOVAL YOU MEANT IS ALLOWED, because you said so. This is the
// whole asymmetry the check rests on: a deliberate removal is already
// worth a sentence, an accidental one never has one.
func TestTheGuardAllowsARemovalThatSaysSo(t *testing.T) {
	dir, base := staleRepo(t)
	git(t, dir, "commit", "-q", "--amend", "-m",
		"my own change, and internal/theirs.go is removed because it is dead")
	if code, out := runGuard(t, dir, base); code != 0 {
		t.Errorf("the guard refused a removal the commit message names:\n%s", out)
	}
}

// ORDINARY WORK IS NOT DISTURBED. A branch that only adds and edits has
// nothing to acknowledge, and must never be asked to.
func TestTheGuardIsQuietOnOrdinaryWork(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	git(t, dir, "checkout", "-q", "-b", "work")
	write(t, dir, "a.go", "package a\n\n"+strings.Repeat("// more\n", 80))
	write(t, dir, "b.go", "package a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "add and extend")
	if code, out := runGuard(t, dir, base); code != 0 {
		t.Errorf("the guard fired on a branch that removed nothing:\n%s", out)
	}
}

// AND A RENAME IS A RENAME. Moving a file is not removing it, and a
// check that could not tell the difference would be turned off within
// a week.
func TestTheGuardDoesNotCallARenameARemoval(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "old.go", strings.Repeat("// content\n", 60))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	git(t, dir, "checkout", "-q", "-b", "move")
	git(t, dir, "mv", "old.go", "new.go")
	git(t, dir, "commit", "-qm", "move it")
	if code, out := runGuard(t, dir, base); code != 0 {
		t.Errorf("the guard called a rename a removal:\n%s", out)
	}
}

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE WALL REFUSES THE LOG DIRECTORY BY ITS PATH, NOT THE BARE WORD IN A
// PATTERN. A pattern command's first operand is
// a pattern, never a file; every spelling of the directory's path is
// refused as before, and so is the word wherever it can be a path.
func TestAPatternIsNotAPathButTheLogDirectoryIs(t *testing.T) {
	r, policy, home := newWordWall(t)
	if err := os.WriteFile(filepath.Join(home, "log", "aii.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("the log word"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "log"), filepath.Join(home, "lg")); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"grep -c log notes.txt", `grep -c "log" notes.txt`, "grep -rn log .", "grep -rn -- log notes.txt",
		"rg log notes.txt", "cat notes.txt | grep log", "cat notes.txt | grep -i log | wc -l",
		`grep -r --include="*.log" x .`, "egrep -w log notes.txt", "git log --oneline -3",
		// a review, 2026-11-06, on e6f4907d: options after the operand that take no word.
		"grep log notes.txt -n", `grep -r log . --include="*.txt"`, "grep log notes.txt -- -e",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q: a pattern was refused as a path: %s", cmd, why)
		}
	}
	// The directory, by every spelling of its path, and the word where it
	// can be a path: refused, in the words the wall always used.
	for _, cmd := range []string{
		"cat log", "ls log", "cat log/aii.log", "cat ./log/aii.log", "cat ../" + filepath.Base(home) + "/log/aii.log",
		"cat lg/aii.log", "ls lg", "grep -r x log", "grep -r x log/", "grep log log", "grep -f log notes.txt",
		"grep -e x log", "grep -m 1 x log", "grep --file log notes.txt", "cd log && cat aii.log", "echo log | sh",
		"grep log > log", "sed -n p log", "awk '{print}' log", "cat notes.txt | grep x log",
		// a review, 2026-11-06: the pattern supplied by an option, and an empty one.
		"grep -r --regexp=x log", "grep -r --file=notes.txt log", "grep -r '' log", `grep -r "" log`, "grep --max-count=1 x log",
		// a review, 2026-11-06, on e6f4907d: the pattern supplied after the operand (GNU grep permutes).
		"grep -r log -e x", "grep -r log --regexp=x", "grep -r log -f notes.txt", "grep log notes.txt --file=notes.txt", "grep -r log -m 1",
	} {
		why, _ := r.shellWallFrom(policy, home, cmd)
		if why == "" || !strings.Contains(why, "log") {
			t.Errorf("%q: the log directory was not refused by its path: %q", cmd, why)
		}
	}
}
